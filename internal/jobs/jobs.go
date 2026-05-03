package jobs

import (
	"bufio"
	"bytes"
	"context"
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
	if preset == "" {
		preset = "balanced"
	}
	opts = media.ApplyPreset(targetFormat, preset, opts)
	if err := media.Validate(*upload.MediaType, targetFormat, preset, opts); err != nil {
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
	s.cancelMu.Lock()
	cancel := s.cancelers[jobID]
	s.cancelMu.Unlock()
	if cancel != nil {
		cancel()
	}
	return s.store.CancelJob(ctx, jobID)
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
	if job.ErrorMessage != nil {
		out["error"] = *job.ErrorMessage
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
	if current != nil && current.Status == "canceled" {
		_ = os.Remove(outputPath)
		_ = s.event(context.Background(), "info", "job.canceled", upload.OwnerUserID, &upload.ID, &job.ID, "job canceled", upload.IPAddress, upload.UserAgent, nil)
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
			switch format {
			case "mp4", "mov", "mkv":
				args = append(args, "-c:v", "libx264", "-profile:v", "baseline", "-level", "3.0", "-pix_fmt", "yuv420p")
			case "webm":
				args = append(args, "-c:v", "libvpx-vp9")
			case "avi":
				args = append(args, "-c:v", "mpeg4")
			}
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
			if opts.VideoBitrate != 0 {
				args = append(args, "-b:v", fmt.Sprintf("%dk", opts.VideoBitrate))
			}
			switch format {
			case "mp4", "mov", "mkv", "avi":
				args = append(args, "-c:a", "aac")
			case "webm":
				args = append(args, "-c:a", "libopus")
			}
			if opts.AudioBitrate != 0 {
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
