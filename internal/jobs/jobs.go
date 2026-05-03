package jobs

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/kirari04/cloudconv/internal/artifacts"
	"github.com/kirari04/cloudconv/internal/config"
	"github.com/kirari04/cloudconv/internal/media"
	"github.com/kirari04/cloudconv/internal/store"
)

type Service struct {
	cfg       config.Config
	store     *store.Store
	cancelMu  sync.Mutex
	cancelers map[string]context.CancelFunc
}

func New(cfg config.Config, s *store.Store) *Service {
	return &Service{
		cfg:       cfg,
		store:     s,
		cancelers: make(map[string]context.CancelFunc),
	}
}

func (s *Service) Start(ctx context.Context) {
	go s.cleanupWorker(ctx)
	go func() {
		ticker := time.NewTicker(time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				limit, err := s.store.SettingInt64(ctx, "max_concurrent_jobs")
				if err != nil || limit < 1 {
					limit = 1
				}
				active, err := s.store.CountJobsByStatus(ctx, "converting")
				if err != nil || int64(active) >= limit {
					continue
				}
				job, err := s.store.ClaimNextJob(ctx)
				if err != nil {
					continue
				}
				go s.convert(context.Background(), job)
			}
		}
	}()
}

func (s *Service) Create(ctx context.Context, upload *store.Upload, targetFormat, preset string, opts media.Options) (*store.Job, string, error) {
	if upload.MediaType == nil || upload.SourcePath == nil {
		return nil, "", errors.New("upload has not been completed")
	}
	targetFormat = strings.ToLower(strings.TrimSpace(targetFormat))
	if preset == "" {
		preset = "balanced"
	}
	opts = media.ApplyPreset(targetFormat, preset, opts)
	opts.EffectiveVideoEncoder = ""
	opts.EffectiveAudioEncoder = ""
	if err := media.Validate(*upload.MediaType, targetFormat, preset, opts); err != nil {
		return nil, "", err
	}
	opts, err := media.ResolveEffectiveCodecs(ctx, targetFormat, opts)
	if err != nil {
		return nil, "", err
	}
	token := ""
	tokenHash := upload.AnonymousTokenHash
	if tokenHash != nil {
		// The plain token is known only to the caller. The API response will use the token
		// it received from upload completion rather than asking the job layer to expose it.
		token = ""
	}
	now := time.Now().UTC()
	optionsJSON, _ := json.Marshal(opts)
	job := store.Job{
		ID:                 uuid.NewString(),
		UploadID:           upload.ID,
		OwnerUserID:        upload.OwnerUserID,
		AnonymousTokenHash: tokenHash,
		Status:             "queued",
		TargetFormat:       targetFormat,
		Preset:             preset,
		OptionsJSON:        string(optionsJSON),
		CreatedAt:          now,
		UpdatedAt:          now,
	}
	if err := s.store.CreateJob(ctx, job); err != nil {
		return nil, "", err
	}
	_ = s.event(ctx, "info", "job.queued", upload.OwnerUserID, &upload.ID, &job.ID, "job queued", upload.IPAddress, upload.UserAgent, nil)
	return &job, token, nil
}

func (s *Service) Cancel(ctx context.Context, jobID string) error {
	job, err := s.store.JobByID(ctx, jobID)
	if err != nil {
		return err
	}
	if job.Status != "queued" && job.Status != "converting" {
		return store.ErrTerminalState
	}
	s.cancelMu.Lock()
	cancel := s.cancelers[jobID]
	s.cancelMu.Unlock()
	if cancel != nil {
		cancel()
	}
	result := artifacts.DeleteJobArtifacts(job, s.cfg.ConvertedDir)
	if result.ErrorString() != nil {
		return fmt.Errorf("could not delete partial output: %s", *result.ErrorString())
	}
	return s.store.CancelJob(ctx, jobID)
}

func (s *Service) CancelForAdmin(ctx context.Context, jobID, adminUserID, note string) (*store.Job, error) {
	job, err := s.store.JobByID(ctx, jobID)
	if err != nil {
		return nil, err
	}
	if job.Status != "queued" && job.Status != "converting" {
		return nil, store.ErrTerminalState
	}
	s.cancelMu.Lock()
	cancel := s.cancelers[jobID]
	s.cancelMu.Unlock()
	if cancel != nil {
		cancel()
	}
	cleanup := artifacts.DeleteJobArtifacts(job, s.cfg.ConvertedDir)
	artifactError := cleanup.ErrorString()
	updated, err := s.store.CancelJobForAdmin(ctx, jobID, adminUserID, note, artifactError)
	if err != nil {
		return nil, err
	}
	upload, _ := s.store.UploadByID(ctx, job.UploadID)
	var ip, ua string
	if upload != nil {
		ip = upload.IPAddress
		ua = upload.UserAgent
	}
	metadata := eventMetadata(map[string]any{
		"adminUserId": adminUserID,
		"note":        note,
		"deleted":     cleanup.Deleted,
		"errors":      cleanup.Errors,
	})
	_ = s.event(ctx, "info", "job.canceled", &adminUserID, &job.UploadID, &job.ID, "job canceled by admin", ip, ua, metadata)
	return updated, nil
}

func (s *Service) Remove(ctx context.Context, jobID, adminUserID, note string) (*store.Job, bool, *string, error) {
	job, err := s.store.JobByID(ctx, jobID)
	if err != nil {
		return nil, false, nil, err
	}
	if job.Status == "removed" {
		return nil, false, nil, store.ErrTerminalState
	}
	if job.Status == "queued" || job.Status == "converting" {
		if _, err := s.CancelForAdmin(ctx, jobID, adminUserID, note); err != nil && !errors.Is(err, store.ErrTerminalState) {
			return nil, false, nil, err
		}
		job, err = s.store.JobByID(ctx, jobID)
		if err != nil {
			return nil, false, nil, err
		}
	}
	cleanup := artifacts.DeleteJobArtifacts(job, s.cfg.ConvertedDir)
	if active, err := s.store.NonRemovedActiveJobsByUploadID(ctx, job.UploadID); err != nil {
		cleanup.Errors = append(cleanup.Errors, err.Error())
	} else if len(active) == 0 {
		if upload, err := s.store.UploadByID(ctx, job.UploadID); err == nil {
			cleanup.Merge(artifacts.DeleteUploadArtifacts(upload, s.cfg.UploadDir))
		} else if !errors.Is(err, sql.ErrNoRows) {
			cleanup.Errors = append(cleanup.Errors, err.Error())
		}
	}
	artifactError := cleanup.ErrorString()
	updated, err := s.store.MarkJobRemoved(ctx, jobID, adminUserID, note, artifactError)
	if err != nil {
		return nil, false, nil, err
	}
	metadata := eventMetadata(map[string]any{
		"adminUserId": adminUserID,
		"uploadId":    job.UploadID,
		"note":        note,
		"deleted":     cleanup.Deleted,
		"errors":      cleanup.Errors,
	})
	upload, _ := s.store.UploadByID(ctx, job.UploadID)
	var ip, ua string
	if upload != nil {
		ip = upload.IPAddress
		ua = upload.UserAgent
	}
	_ = s.event(ctx, "info", "job.removed", &adminUserID, &job.UploadID, &job.ID, "job removed by admin", ip, ua, metadata)
	return updated, artifactError == nil, artifactError, nil
}

func (s *Service) JobResponse(ctx context.Context, job *store.Job, token string) map[string]any {
	out := map[string]any{
		"id":                 job.ID,
		"uploadId":           job.UploadID,
		"status":             job.Status,
		"targetFormat":       job.TargetFormat,
		"preset":             job.Preset,
		"progressPercentage": job.ProgressPercentage,
		"createdAt":          job.CreatedAt,
		"updatedAt":          job.UpdatedAt,
	}
	if job.Status == "queued" {
		if pos, err := s.store.QueuePosition(ctx, job.ID); err == nil {
			out["queuePosition"] = pos
		}
	}
	if job.Status == "error" {
		out["error"] = "Conversion failed."
	}
	if job.Status == "finished" {
		url := "/download/" + job.ID
		if token != "" {
			url += "?token=" + token
		}
		out["downloadUrl"] = url
		if job.OutputSizeBytes != nil {
			out["outputSizeBytes"] = *job.OutputSizeBytes
		}
	}
	return out
}

func (s *Service) convert(parent context.Context, job *store.Job) {
	ctx := parent
	timeoutMinutes, err := s.store.SettingInt64(ctx, "conversion_timeout_minutes")
	if err != nil || timeoutMinutes < 1 {
		timeoutMinutes = 240
	}
	ctx, cancel := context.WithTimeout(parent, time.Duration(timeoutMinutes)*time.Minute)
	defer cancel()
	s.cancelMu.Lock()
	s.cancelers[job.ID] = cancel
	s.cancelMu.Unlock()
	defer func() {
		s.cancelMu.Lock()
		delete(s.cancelers, job.ID)
		s.cancelMu.Unlock()
	}()

	upload, err := s.store.UploadByID(ctx, job.UploadID)
	if err != nil || upload.SourcePath == nil {
		_ = s.store.FailJob(context.Background(), job.ID, "upload source file is missing")
		return
	}
	opts := media.DecodeOptions(job.OptionsJSON)
	opts, err = resolveMissingEffectiveCodecs(ctx, job.TargetFormat, opts)
	if err != nil {
		message := "conversion failed: " + err.Error()
		_ = s.store.FailJob(context.Background(), job.ID, message)
		_ = s.event(context.Background(), "error", "job.failed", upload.OwnerUserID, &upload.ID, &job.ID, message, upload.IPAddress, upload.UserAgent, nil)
		return
	}
	outputPath := filepath.Join(s.cfg.ConvertedDir, job.ID+"."+media.ExtensionFor(job.TargetFormat))
	if err := os.MkdirAll(s.cfg.ConvertedDir, 0755); err != nil {
		_ = s.store.FailJob(context.Background(), job.ID, "could not prepare output directory")
		return
	}
	socketPath := filepath.Join(os.TempDir(), job.ID+".sock")
	_ = os.Remove(socketPath)
	listener, err := net.Listen("unix", socketPath)
	if err != nil {
		_ = s.store.FailJob(context.Background(), job.ID, "failed to initialize progress monitoring")
		return
	}
	defer func() {
		listener.Close()
		_ = os.Remove(socketPath)
	}()
	totalDuration := getFileDuration(ctx, *upload.SourcePath)
	progressDone := make(chan struct{})
	go func() {
		defer close(progressDone)
		monitorProgress(listener, s.store, job.ID, totalDuration)
	}()
	cmd := buildFFmpegCommand(ctx, job, upload, opts, *upload.SourcePath, outputPath, socketPath)
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = s.store.FailJob(context.Background(), job.ID, "failed to capture converter output")
		return
	}
	var capped cappedBuffer
	if err := cmd.Start(); err != nil {
		_ = s.store.FailJob(context.Background(), job.ID, "failed to start converter")
		return
	}
	copyDone := make(chan struct{})
	go func() {
		_, _ = io.Copy(&capped, stderr)
		close(copyDone)
	}()
	err = cmd.Wait()
	<-copyDone
	listener.Close()
	select {
	case <-progressDone:
	case <-time.After(2 * time.Second):
	}
	current, _ := s.store.JobByID(context.Background(), job.ID)
	if current != nil && (current.Status == "canceled" || current.Status == "removed") {
		_ = os.Remove(outputPath)
		if current.Status == "canceled" {
			_ = s.event(context.Background(), "info", "job.canceled", upload.OwnerUserID, &upload.ID, &job.ID, "job canceled", upload.IPAddress, upload.UserAgent, nil)
		}
		return
	}
	if err != nil {
		message := "conversion failed"
		if ctx.Err() == context.DeadlineExceeded {
			message = "conversion timed out"
		}
		if details := strings.TrimSpace(capped.String()); details != "" {
			message += ": " + truncate(details, 1200)
		}
		_ = s.store.FailJob(context.Background(), job.ID, message)
		_ = s.event(context.Background(), "error", "job.failed", upload.OwnerUserID, &upload.ID, &job.ID, message, upload.IPAddress, upload.UserAgent, nil)
		_ = os.Remove(outputPath)
		return
	}
	info, statErr := os.Stat(outputPath)
	if statErr != nil {
		_ = s.store.FailJob(context.Background(), job.ID, "conversion finished but output file was not found")
		return
	}
	_ = s.store.FinishJob(context.Background(), job.ID, outputPath, info.Size())
	_ = os.Remove(*upload.SourcePath)
	_ = s.event(context.Background(), "info", "job.finished", upload.OwnerUserID, &upload.ID, &job.ID, "job finished", upload.IPAddress, upload.UserAgent, nil)
}

func buildFFmpegCommand(ctx context.Context, job *store.Job, upload *store.Upload, opts media.Options, inputPath, outputPath, socketPath string) *exec.Cmd {
	args := []string{"-nostdin", "-hide_banner", "-i", inputPath, "-progress", "unix://" + socketPath}
	format := job.TargetFormat
	switch media.OutputKind(format) {
	case "video":
		if format == "gif" {
			args = append(args, "-map", "0:v:0", "-an")
			loop := "0"
			if !opts.Loop {
				loop = "-1"
			}
			fps := defaultInt(opts.Framerate, 15)
			width := defaultInt(opts.MaxWidth, 480)
			filter := fmt.Sprintf("fps=%d,scale=%d:-2:flags=lanczos,split[s0][s1];[s0]palettegen[p];[s1][p]paletteuse", fps, width)
			args = append(args, "-loop", loop, "-vf", filter)
		} else {
			args = append(args, "-map", "0:v:0", "-map", "0:a?")
			videoCodec := resolveVideoCodec(ctx, format, opts)
			audioCodec := resolveAudioCodec(ctx, format, opts)
			args = appendVideoCodecArgs(args, format, job.Preset, videoCodec, opts)
			var vf []string
			if opts.MaxHeight != 0 {
				vf = append(vf, fmt.Sprintf("scale=-2:%d", opts.MaxHeight))
			}
			if upload.OriginalFilename != "" && strings.HasSuffix(strings.ToLower(upload.OriginalFilename), ".gif") {
				vf = append(vf, "format=yuv420p")
			}
			if len(vf) > 0 {
				args = append(args, "-vf", strings.Join(vf, ","))
			}
			if opts.Framerate != 0 {
				args = append(args, "-r", strconv.Itoa(opts.Framerate))
			}
			if opts.VideoBitrate != 0 && videoCodec.ID != "av1" {
				args = append(args, "-b:v", fmt.Sprintf("%dk", opts.VideoBitrate))
			}
			args = appendAudioCodecArgs(args, format, audioCodec)
			if opts.AudioBitrate != 0 && audioCodec.SupportsBitrate {
				args = append(args, "-b:a", fmt.Sprintf("%dk", opts.AudioBitrate))
			}
			if format == "mp4" {
				args = append(args, "-movflags", "+faststart")
			}
		}
	case "audio":
		args = append(args, "-map", "0:a?")
		switch format {
		case "mp3":
			args = append(args, "-c:a", "libmp3lame")
		case "wav":
			args = append(args, "-c:a", "pcm_s16le")
		case "ogg":
			args = append(args, "-c:a", "libvorbis")
		case "flac":
			args = append(args, "-c:a", "flac")
		}
		if opts.AudioBitrate != 0 && format != "wav" && format != "flac" {
			args = append(args, "-b:a", fmt.Sprintf("%dk", opts.AudioBitrate))
		}
	case "image":
		args = append(args, "-map", "0:v:0", "-frames:v", "1")
		var vf []string
		if opts.MaxWidth != 0 {
			vf = append(vf, fmt.Sprintf("scale=%d:-1", opts.MaxWidth))
		}
		if len(vf) > 0 {
			args = append(args, "-vf", strings.Join(vf, ","))
		}
		if opts.Quality != 0 {
			switch format {
			case "jpg", "jpeg":
				q := 31 - ((opts.Quality * 29) / 100)
				if q < 2 {
					q = 2
				}
				args = append(args, "-q:v", strconv.Itoa(q))
			case "webp":
				args = append(args, "-quality", strconv.Itoa(opts.Quality))
			}
		}
	}
	args = append(args, "-y", outputPath)
	return exec.CommandContext(ctx, "ffmpeg", args...)
}

func resolveMissingEffectiveCodecs(ctx context.Context, format string, opts media.Options) (media.Options, error) {
	if media.OutputKind(format) != "video" || format == "gif" {
		return opts, nil
	}
	videoCodec := media.ResolveVideoCodec(format, opts)
	if videoCodec.ID != "" && opts.EffectiveVideoEncoder == "" {
		encoder, ok := media.ResolveRuntimeCodecEncoder(ctx, videoCodec)
		if !ok {
			return opts, fmt.Errorf("video codec %s is not available on this server", videoCodec.ID)
		}
		opts.EffectiveVideoEncoder = encoder
	}
	audioCodec := media.ResolveAudioCodec(format, opts)
	if audioCodec.ID != "" && opts.EffectiveAudioEncoder == "" {
		encoder, ok := media.ResolveRuntimeCodecEncoder(ctx, audioCodec)
		if !ok {
			return opts, fmt.Errorf("audio codec %s is not available on this server", audioCodec.ID)
		}
		opts.EffectiveAudioEncoder = encoder
	}
	return opts, nil
}

func resolveVideoCodec(ctx context.Context, format string, opts media.Options) media.CodecChoice {
	codec := media.ResolveVideoCodec(format, opts)
	if opts.EffectiveVideoEncoder != "" {
		codec.Encoder = opts.EffectiveVideoEncoder
	} else if encoder, ok := media.ResolveRuntimeCodecEncoder(ctx, codec); ok {
		codec.Encoder = encoder
	}
	return codec
}

func resolveAudioCodec(ctx context.Context, format string, opts media.Options) media.CodecChoice {
	codec := media.ResolveAudioCodec(format, opts)
	if opts.EffectiveAudioEncoder != "" {
		codec.Encoder = opts.EffectiveAudioEncoder
	} else if encoder, ok := media.ResolveRuntimeCodecEncoder(ctx, codec); ok {
		codec.Encoder = encoder
	}
	return codec
}

func appendVideoCodecArgs(args []string, format, preset string, codec media.CodecChoice, opts media.Options) []string {
	switch codec.ID {
	case "h264":
		args = append(args, "-c:v", codec.Encoder, "-profile:v", "baseline", "-level", "3.0", "-pix_fmt", "yuv420p")
	case "h265":
		args = append(args, "-c:v", codec.Encoder, "-pix_fmt", "yuv420p")
		if format == "mp4" || format == "mov" {
			args = append(args, "-tag:v", "hvc1")
		}
	case "av1":
		args = appendAV1CodecArgs(args, preset, codec.Encoder, opts.VideoBitrate)
	case "vp9", "vp8", "mpeg4":
		args = append(args, "-c:v", codec.Encoder)
	}
	return args
}

func appendAV1CodecArgs(args []string, preset, encoder string, videoBitrate int) []string {
	args = append(args, "-c:v", encoder)
	switch encoder {
	case "libsvtav1":
		args = append(args, "-preset", av1PresetValue(preset, "10", "8", "6"))
		if videoBitrate != 0 {
			args = append(args, "-b:v", fmt.Sprintf("%dk", videoBitrate))
		} else {
			args = append(args, "-crf", av1PresetValue(preset, "38", "34", "30"))
		}
		args = append(args, "-pix_fmt", "yuv420p")
	case "librav1e":
		args = append(args, "-speed", av1PresetValue(preset, "10", "8", "6"))
		if videoBitrate != 0 {
			args = append(args, "-b:v", fmt.Sprintf("%dk", videoBitrate))
		} else {
			args = append(args, "-qp", av1PresetValue(preset, "140", "120", "100"))
		}
		args = append(args, "-pix_fmt", "yuv420p")
	case "libaom-av1":
		args = append(args, "-cpu-used", av1PresetValue(preset, "8", "6", "4"), "-row-mt", "1", "-threads", "0")
		if videoBitrate != 0 {
			args = append(args, "-b:v", fmt.Sprintf("%dk", videoBitrate))
		} else {
			args = append(args, "-crf", av1PresetValue(preset, "40", "36", "32"))
		}
		args = append(args, "-pix_fmt", "yuv420p")
	}
	return args
}

func av1PresetValue(preset, small, balanced, high string) string {
	switch preset {
	case "small":
		return small
	case "high":
		return high
	default:
		return balanced
	}
}

func appendAudioCodecArgs(args []string, _ string, codec media.CodecChoice) []string {
	if codec.ID == "" {
		return args
	}
	return append(args, "-c:a", codec.Encoder)
}

func getFileDuration(ctx context.Context, filePath string) float64 {
	cmd := exec.CommandContext(ctx, "ffprobe", "-v", "error", "-show_entries", "format=duration", "-of", "default=noprint_wrappers=1:nokey=1", filePath)
	output, err := cmd.Output()
	if err != nil {
		return 0
	}
	durationStr := strings.TrimSpace(string(output))
	if durationStr == "" || durationStr == "N/A" {
		return 0
	}
	duration, _ := strconv.ParseFloat(durationStr, 64)
	return duration
}

func monitorProgress(listener net.Listener, st *store.Store, jobID string, totalDuration float64) {
	conn, err := listener.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	scanner := bufio.NewScanner(conn)
	for scanner.Scan() {
		parts := strings.SplitN(scanner.Text(), "=", 2)
		if len(parts) != 2 {
			continue
		}
		switch parts[0] {
		case "out_time_ms":
			currentTimeMs, err := strconv.ParseInt(parts[1], 10, 64)
			if err == nil && totalDuration > 0 {
				progress := int(((float64(currentTimeMs) / 1000000.0) / totalDuration) * 100)
				if progress > 100 {
					progress = 100
				}
				_ = st.UpdateJobProgress(context.Background(), jobID, progress)
			}
		case "progress":
			if parts[1] == "end" {
				_ = st.UpdateJobProgress(context.Background(), jobID, 100)
			}
		}
	}
}

func (s *Service) cleanupWorker(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Minute)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			timeoutMinutes, err := s.store.SettingInt64(ctx, "upload_inactivity_timeout_minutes")
			if err != nil || timeoutMinutes < 1 {
				timeoutMinutes = 30
			}
			inactive, err := s.store.CancelInactiveUploads(ctx, time.Now().UTC().Add(-time.Duration(timeoutMinutes)*time.Minute))
			if err == nil {
				for _, upload := range inactive {
					_ = os.RemoveAll(filepath.Join(s.cfg.UploadDir, upload.ID))
					_ = s.event(ctx, "info", "upload.canceled", upload.OwnerUserID, &upload.ID, nil, "upload canceled after inactivity", upload.IPAddress, upload.UserAgent, nil)
				}
			}
			retentionHours, err := s.store.SettingInt64(ctx, "failed_upload_retention_hours")
			if err != nil || retentionHours < 1 {
				retentionHours = 24
			}
			paths, err := s.store.CleanupExpired(ctx, time.Now().UTC().Add(-time.Duration(retentionHours)*time.Hour))
			if err == nil {
				for _, path := range paths {
					_ = os.Remove(path)
				}
			}
			_ = s.store.CleanupSessions(ctx)
		}
	}
}

func (s *Service) event(ctx context.Context, level, kind string, actor, uploadID, jobID *string, message, ip, ua string, metadata *string) error {
	return s.store.AddEvent(ctx, store.Event{
		Level:        level,
		Kind:         kind,
		ActorUserID:  actor,
		UploadID:     uploadID,
		JobID:        jobID,
		Message:      message,
		MetadataJSON: metadata,
		IPAddress:    &ip,
		UserAgent:    &ua,
		CreatedAt:    time.Now().UTC(),
	})
}

func eventMetadata(value map[string]any) *string {
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	out := string(data)
	return &out
}

type cappedBuffer struct {
	buf bytes.Buffer
}

func (b *cappedBuffer) Write(p []byte) (int, error) {
	const max = 8192
	if b.buf.Len()+len(p) <= max {
		return b.buf.Write(p)
	}
	remaining := max - b.buf.Len()
	if remaining > 0 {
		_, _ = b.buf.Write(p[:remaining])
	}
	return len(p), nil
}

func (b *cappedBuffer) String() string {
	return b.buf.String()
}

func defaultInt(value, fallback int) int {
	if value == 0 {
		return fallback
	}
	return value
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}
