package uploads

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"

	"github.com/kirari04/cloudconv/internal/artifacts"
	"github.com/kirari04/cloudconv/internal/auth"
	"github.com/kirari04/cloudconv/internal/config"
	"github.com/kirari04/cloudconv/internal/media"
	"github.com/kirari04/cloudconv/internal/store"
)

type Service struct {
	cfg   config.Config
	store *store.Store
}

type InitiateRequest struct {
	Filename string `json:"filename"`
	Size     int64  `json:"size"`
	MIME     string `json:"mime"`
}

type InitiateResponse struct {
	UploadID       string `json:"uploadId"`
	ChunkSizeBytes int64  `json:"chunkSizeBytes"`
	ChunkCount     int    `json:"chunkCount"`
	MissingChunks  []int  `json:"missingChunks"`
	Token          string `json:"token,omitempty"`
}

type CompleteRequest struct {
	TargetFormat string        `json:"targetFormat"`
	Preset       string        `json:"preset"`
	Options      media.Options `json:"options"`
}

type AdminCancelResult struct {
	Upload           *store.Upload `json:"upload"`
	CanceledJobIDs   []string      `json:"canceledJobIds"`
	ArtifactsDeleted bool          `json:"artifactsDeleted"`
	ArtifactError    *string       `json:"artifactError"`
}

type AdminJobCancelFunc func(ctx context.Context, jobID, adminUserID, note string) (*store.Job, error)

func New(cfg config.Config, s *store.Store) *Service {
	return &Service{cfg: cfg, store: s}
}

func (u *Service) Initiate(ctx context.Context, req InitiateRequest, user *store.User, ip, userAgent string) (InitiateResponse, error) {
	req.Filename = sanitizeFilename(req.Filename)
	if req.Filename == "" {
		return InitiateResponse{}, errors.New("filename is required")
	}
	if req.Size <= 0 {
		return InitiateResponse{}, errors.New("file size must be greater than zero")
	}
	publicEnabled, err := u.store.SettingBool(ctx, "public_uploads_enabled")
	if err != nil {
		return InitiateResponse{}, err
	}
	if !publicEnabled && user == nil {
		return InitiateResponse{}, errors.New("login is required before uploading")
	}
	maxUpload, err := u.store.SettingInt64(ctx, "max_upload_bytes")
	if err != nil {
		return InitiateResponse{}, err
	}
	if req.Size > maxUpload {
		return InitiateResponse{}, fmt.Errorf("file exceeds maximum upload size of %d bytes", maxUpload)
	}
	if err := u.checkLimits(ctx, ip); err != nil {
		return InitiateResponse{}, err
	}
	chunkSize, err := u.store.SettingInt64(ctx, "chunk_size_bytes")
	if err != nil {
		return InitiateResponse{}, err
	}
	if chunkSize <= 0 {
		chunkSize = 16 * 1024 * 1024
	}
	chunkCount := int((req.Size + chunkSize - 1) / chunkSize)
	var owner *string
	var tokenPlain string
	var tokenHash *string
	if user != nil {
		owner = &user.ID
	} else {
		plain, hash := auth.NewAnonymousToken()
		tokenPlain = plain
		tokenHash = &hash
	}
	now := time.Now().UTC()
	upload := store.Upload{
		ID:                 uuid.NewString(),
		OwnerUserID:        owner,
		AnonymousTokenHash: tokenHash,
		OriginalFilename:   req.Filename,
		SizeBytes:          req.Size,
		BytesReceived:      0,
		ChunkSizeBytes:     chunkSize,
		ChunkCount:         chunkCount,
		Status:             "uploading",
		IPAddress:          ip,
		UserAgent:          userAgent,
		CreatedAt:          now,
		UpdatedAt:          now,
		ExpiresAt:          now.Add(24 * time.Hour),
	}
	if err := os.MkdirAll(filepath.Join(u.cfg.UploadDir, upload.ID, "chunks"), 0755); err != nil {
		return InitiateResponse{}, err
	}
	if err := u.store.CreateUpload(ctx, upload); err != nil {
		return InitiateResponse{}, err
	}
	_ = u.event(ctx, "info", "upload.created", nil, &upload.ID, nil, "upload session created", ip, userAgent)
	return InitiateResponse{
		UploadID:       upload.ID,
		ChunkSizeBytes: chunkSize,
		ChunkCount:     chunkCount,
		MissingChunks:  rangeInts(chunkCount),
		Token:          tokenPlain,
	}, nil
}

func (u *Service) SaveChunk(ctx context.Context, uploadID string, index int, body io.Reader, contentLength int64, contentRange, shaHeader string, user *store.User, token string) error {
	upload, err := u.store.UploadByID(ctx, uploadID)
	if err != nil {
		return err
	}
	if err := AuthorizeUpload(upload, user, token); err != nil {
		return err
	}
	if upload.Status != "uploading" {
		return errors.New("upload is not accepting chunks")
	}
	expired, err := u.expireIfInactive(ctx, upload)
	if err != nil {
		return err
	}
	if expired {
		return errors.New("upload session expired due to inactivity")
	}
	if err := u.store.TouchUpload(ctx, uploadID); err != nil {
		return err
	}
	if index < 0 || index >= upload.ChunkCount {
		return errors.New("chunk index out of range")
	}
	if contentRange == "" {
		return errors.New("Content-Range is required")
	}
	start, end, total, err := parseContentRange(contentRange)
	if err != nil {
		return err
	}
	if total != upload.SizeBytes {
		return errors.New("Content-Range total does not match upload size")
	}
	expectedStart := int64(index) * upload.ChunkSizeBytes
	expectedEnd := expectedStart + upload.ChunkSizeBytes - 1
	if expectedEnd >= upload.SizeBytes {
		expectedEnd = upload.SizeBytes - 1
	}
	if start != expectedStart || end != expectedEnd {
		return errors.New("Content-Range does not match chunk index")
	}
	expectedSize := end - start + 1
	if contentLength >= 0 && contentLength != expectedSize {
		return errors.New("chunk size does not match Content-Range")
	}
	chunkDir := filepath.Join(u.cfg.UploadDir, upload.ID, "chunks")
	if err := os.MkdirAll(chunkDir, 0755); err != nil {
		return err
	}
	path := filepath.Join(chunkDir, fmt.Sprintf("%06d.part", index))
	tmpPath := path + ".tmp"
	dst, err := os.Create(tmpPath)
	if err != nil {
		return err
	}
	hasher := sha256.New()
	reader := &activityReader{
		r:        io.LimitReader(body, expectedSize+1),
		interval: 30 * time.Second,
		touch: func() {
			_ = u.store.TouchUpload(context.Background(), uploadID)
		},
	}
	written, copyErr := io.Copy(dst, io.TeeReader(reader, hasher))
	closeErr := dst.Close()
	if copyErr != nil {
		os.Remove(tmpPath)
		return copyErr
	}
	if closeErr != nil {
		os.Remove(tmpPath)
		return closeErr
	}
	if written != expectedSize {
		os.Remove(tmpPath)
		return errors.New("chunk body size did not match expected size")
	}
	sum := base64.RawURLEncoding.EncodeToString(hasher.Sum(nil))
	if shaHeader != "" && !equalHash(shaHeader, sum) {
		os.Remove(tmpPath)
		return errors.New("chunk checksum mismatch")
	}
	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath)
		return err
	}
	shaPtr := &sum
	return u.store.UpdateUploadChunk(ctx, uploadID, store.UploadChunk{
		UploadID:   uploadID,
		Index:      index,
		SizeBytes:  expectedSize,
		SHA256:     shaPtr,
		Path:       path,
		ReceivedAt: time.Now().UTC(),
	})
}

func (u *Service) Status(ctx context.Context, uploadID string, user *store.User, token string) (*store.Upload, []int, error) {
	upload, err := u.store.UploadByID(ctx, uploadID)
	if err != nil {
		return nil, nil, err
	}
	if err := AuthorizeUpload(upload, user, token); err != nil {
		return nil, nil, err
	}
	_, _ = u.expireIfInactive(ctx, upload)
	missing, err := u.store.MissingChunks(ctx, upload.ID, upload.ChunkCount)
	return upload, missing, err
}

func (u *Service) Assemble(ctx context.Context, uploadID string, user *store.User, token string) (*store.Upload, media.ProbeInfo, error) {
	upload, err := u.store.UploadByID(ctx, uploadID)
	if err != nil {
		return nil, media.ProbeInfo{}, err
	}
	if err := AuthorizeUpload(upload, user, token); err != nil {
		return nil, media.ProbeInfo{}, err
	}
	expired, err := u.expireIfInactive(ctx, upload)
	if err != nil {
		return nil, media.ProbeInfo{}, err
	}
	if expired {
		return nil, media.ProbeInfo{}, errors.New("upload session expired due to inactivity")
	}
	missing, err := u.store.MissingChunks(ctx, upload.ID, upload.ChunkCount)
	if err != nil {
		return nil, media.ProbeInfo{}, err
	}
	if len(missing) > 0 {
		return nil, media.ProbeInfo{}, fmt.Errorf("upload has %d missing chunks", len(missing))
	}
	if err := u.store.UpdateUploadStatus(ctx, upload.ID, "assembling"); err != nil {
		return nil, media.ProbeInfo{}, err
	}
	chunks, err := u.store.Chunks(ctx, upload.ID)
	if err != nil {
		return nil, media.ProbeInfo{}, err
	}
	assembledPath := filepath.Join(u.cfg.UploadDir, upload.ID, "source"+filepath.Ext(upload.OriginalFilename))
	out, err := os.Create(assembledPath)
	if err != nil {
		return nil, media.ProbeInfo{}, err
	}
	for _, chunk := range chunks {
		in, err := os.Open(chunk.Path)
		if err != nil {
			out.Close()
			return nil, media.ProbeInfo{}, err
		}
		if _, err := io.Copy(out, in); err != nil {
			in.Close()
			out.Close()
			return nil, media.ProbeInfo{}, err
		}
		in.Close()
	}
	if err := out.Close(); err != nil {
		return nil, media.ProbeInfo{}, err
	}
	info, err := media.Probe(ctx, assembledPath)
	if err != nil {
		_ = u.store.UpdateUploadStatus(ctx, upload.ID, "error")
		return nil, info, err
	}
	if err := u.store.CompleteUpload(ctx, upload.ID, assembledPath, info.MediaType, info.DetectedMIME); err != nil {
		return nil, info, err
	}
	fresh, err := u.store.UploadByID(ctx, upload.ID)
	if err != nil {
		return nil, info, err
	}
	_ = u.event(ctx, "info", "upload.completed", nil, &upload.ID, nil, "upload assembled and probed", upload.IPAddress, upload.UserAgent)
	return fresh, info, nil
}

func (u *Service) CreateCompletedUpload(ctx context.Context, filename, path string, size int64, user *store.User, tokenHash *string, ip, userAgent string, info media.ProbeInfo) (*store.Upload, error) {
	var owner *string
	if user != nil {
		owner = &user.ID
	}
	now := time.Now().UTC()
	upload := store.Upload{
		ID:                 uuid.NewString(),
		OwnerUserID:        owner,
		AnonymousTokenHash: tokenHash,
		OriginalFilename:   sanitizeFilename(filename),
		SourcePath:         &path,
		MediaType:          &info.MediaType,
		DetectedMIME:       &info.DetectedMIME,
		SizeBytes:          size,
		BytesReceived:      size,
		ChunkSizeBytes:     size,
		ChunkCount:         1,
		Status:             "complete",
		IPAddress:          ip,
		UserAgent:          userAgent,
		CreatedAt:          now,
		UpdatedAt:          now,
		ExpiresAt:          now.Add(24 * time.Hour),
	}
	if err := u.store.CreateUpload(ctx, upload); err != nil {
		return nil, err
	}
	return &upload, nil
}

func (u *Service) AdminCancel(ctx context.Context, uploadID, adminUserID, note string, cancelJob AdminJobCancelFunc) (AdminCancelResult, error) {
	upload, err := u.store.UploadByID(ctx, uploadID)
	if err != nil {
		return AdminCancelResult{}, err
	}
	jobs, err := u.store.JobsByUploadID(ctx, uploadID)
	if err != nil {
		return AdminCancelResult{}, err
	}
	canceledJobIDs := make([]string, 0)
	for _, job := range jobs {
		if job.Status != "queued" && job.Status != "converting" {
			continue
		}
		if cancelJob != nil {
			if _, err := cancelJob(ctx, job.ID, adminUserID, note); err != nil && !errors.Is(err, store.ErrTerminalState) {
				return AdminCancelResult{}, err
			}
		}
		canceledJobIDs = append(canceledJobIDs, job.ID)
	}
	cleanup := artifacts.DeleteUploadArtifacts(upload, u.cfg.UploadDir)
	artifactError := cleanup.ErrorString()
	updated, err := u.store.CancelUploadForAdmin(ctx, uploadID, adminUserID, note, artifactError)
	if err != nil {
		return AdminCancelResult{}, err
	}
	metadata := uploadEventMetadata(map[string]any{
		"adminUserId":    adminUserID,
		"note":           note,
		"canceledJobIds": canceledJobIDs,
		"deleted":        cleanup.Deleted,
		"errors":         cleanup.Errors,
	})
	_ = u.store.AddEvent(ctx, store.Event{
		Level:        "info",
		Kind:         "upload.canceled",
		ActorUserID:  &adminUserID,
		UploadID:     &upload.ID,
		Message:      "upload canceled by admin",
		MetadataJSON: metadata,
		IPAddress:    &upload.IPAddress,
		UserAgent:    &upload.UserAgent,
		CreatedAt:    time.Now().UTC(),
	})
	return AdminCancelResult{
		Upload:           updated,
		CanceledJobIDs:   canceledJobIDs,
		ArtifactsDeleted: artifactError == nil,
		ArtifactError:    artifactError,
	}, nil
}

func AuthorizeUpload(upload *store.Upload, user *store.User, token string) error {
	if upload.OwnerUserID != nil {
		if user == nil {
			return errors.New("login is required")
		}
		if user.Role == "admin" || *upload.OwnerUserID == user.ID {
			return nil
		}
		return errors.New("upload is not accessible")
	}
	if upload.AnonymousTokenHash == nil {
		return nil
	}
	if token == "" {
		return errors.New("upload token is required")
	}
	if auth.HashToken(token) != *upload.AnonymousTokenHash {
		return errors.New("invalid upload token")
	}
	return nil
}

func TokenFromRequest(r *http.Request) string {
	if token := r.Header.Get("X-CloudConv-Token"); token != "" {
		return token
	}
	if token := r.URL.Query().Get("token"); token != "" {
		return token
	}
	return ""
}

func (u *Service) checkLimits(ctx context.Context, ip string) error {
	if err := u.cancelInactiveUploads(ctx); err != nil {
		return err
	}
	queueDepth, err := u.store.CountJobsByStatus(ctx, "queued", "converting")
	if err != nil {
		return err
	}
	maxQueue, err := u.store.SettingInt64(ctx, "max_queue_depth")
	if err != nil {
		return err
	}
	if int64(queueDepth) >= maxQueue {
		return errors.New("conversion queue is currently full")
	}
	maxActive, err := u.store.SettingInt64(ctx, "max_active_uploads_per_ip")
	if err != nil {
		return err
	}
	cutoff, err := u.inactivityCutoff(ctx)
	if err != nil {
		return err
	}
	active, err := u.store.CountActiveUploadsByIP(ctx, ip, cutoff)
	if err != nil {
		return err
	}
	if int64(active) >= maxActive {
		return errors.New("too many active uploads from this IP")
	}
	maxStarts, err := u.store.SettingInt64(ctx, "max_upload_starts_per_ip_per_hour")
	if err != nil {
		return err
	}
	starts, err := u.store.CountUploadsByIPSince(ctx, ip, time.Now().UTC().Add(-time.Hour))
	if err != nil {
		return err
	}
	if int64(starts) >= maxStarts {
		return errors.New("too many upload starts from this IP")
	}
	maxJobs, err := u.store.SettingInt64(ctx, "max_jobs_per_ip_per_day")
	if err != nil {
		return err
	}
	jobs, err := u.store.CountJobsByIPSince(ctx, ip, time.Now().UTC().Add(-24*time.Hour))
	if err != nil {
		return err
	}
	if int64(jobs) >= maxJobs {
		return errors.New("daily conversion limit reached for this IP")
	}
	minFree, err := u.store.SettingInt64(ctx, "min_free_disk_bytes")
	if err != nil {
		return err
	}
	free, err := freeBytes(u.cfg.UploadDir)
	if err != nil {
		return err
	}
	if free < minFree {
		return errors.New("server is low on available disk space")
	}
	return nil
}

func (u *Service) cancelInactiveUploads(ctx context.Context) error {
	cutoff, err := u.inactivityCutoff(ctx)
	if err != nil {
		return err
	}
	inactive, err := u.store.CancelInactiveUploads(ctx, cutoff)
	if err != nil {
		return err
	}
	for _, upload := range inactive {
		_ = os.RemoveAll(filepath.Join(u.cfg.UploadDir, upload.ID))
		_ = u.event(ctx, "info", "upload.canceled", upload.OwnerUserID, &upload.ID, nil, "upload canceled after inactivity", upload.IPAddress, upload.UserAgent)
	}
	return nil
}

func (u *Service) expireIfInactive(ctx context.Context, upload *store.Upload) (bool, error) {
	if upload.Status != "uploading" {
		return false, nil
	}
	cutoff, err := u.inactivityCutoff(ctx)
	if err != nil {
		return false, err
	}
	if !upload.UpdatedAt.Before(cutoff) {
		return false, nil
	}
	if err := u.store.UpdateUploadStatus(ctx, upload.ID, "canceled"); err != nil {
		return false, err
	}
	_ = os.RemoveAll(filepath.Join(u.cfg.UploadDir, upload.ID))
	upload.Status = "canceled"
	_ = u.event(ctx, "info", "upload.canceled", upload.OwnerUserID, &upload.ID, nil, "upload canceled after inactivity", upload.IPAddress, upload.UserAgent)
	return true, nil
}

func (u *Service) inactivityCutoff(ctx context.Context) (time.Time, error) {
	minutes, err := u.store.SettingInt64(ctx, "upload_inactivity_timeout_minutes")
	if err != nil {
		return time.Time{}, err
	}
	if minutes < 1 {
		minutes = 30
	}
	return time.Now().UTC().Add(-time.Duration(minutes) * time.Minute), nil
}

func (u *Service) event(ctx context.Context, level, kind string, actor, uploadID, jobID *string, message, ip, ua string) error {
	return u.store.AddEvent(ctx, store.Event{
		Level:       level,
		Kind:        kind,
		ActorUserID: actor,
		UploadID:    uploadID,
		JobID:       jobID,
		Message:     message,
		IPAddress:   &ip,
		UserAgent:   &ua,
		CreatedAt:   time.Now().UTC(),
	})
}

func uploadEventMetadata(value map[string]any) *string {
	data, err := json.Marshal(value)
	if err != nil {
		return nil
	}
	out := string(data)
	return &out
}

var contentRangeRE = regexp.MustCompile(`^bytes (\d+)-(\d+)/(\d+)$`)

func parseContentRange(value string) (int64, int64, int64, error) {
	matches := contentRangeRE.FindStringSubmatch(value)
	if len(matches) != 4 {
		return 0, 0, 0, errors.New("invalid Content-Range")
	}
	start, _ := strconv.ParseInt(matches[1], 10, 64)
	end, _ := strconv.ParseInt(matches[2], 10, 64)
	total, _ := strconv.ParseInt(matches[3], 10, 64)
	if end < start || total <= end {
		return 0, 0, 0, errors.New("invalid Content-Range bounds")
	}
	return start, end, total, nil
}

func equalHash(header, rawURLEncoded string) bool {
	normalized := strings.TrimSpace(header)
	if normalized == rawURLEncoded {
		return true
	}
	if strings.HasPrefix(normalized, "sha256=") {
		normalized = strings.TrimPrefix(normalized, "sha256=")
	}
	if normalized == rawURLEncoded {
		return true
	}
	decoded, err := base64.StdEncoding.DecodeString(normalized)
	if err == nil {
		return base64.RawURLEncoding.EncodeToString(decoded) == rawURLEncoded
	}
	return false
}

func rangeInts(n int) []int {
	out := make([]int, n)
	for i := range out {
		out[i] = i
	}
	return out
}

func sanitizeFilename(name string) string {
	name = filepath.Base(name)
	name = strings.TrimSpace(name)
	if name == "." || name == "/" {
		return ""
	}
	name = strings.Map(func(r rune) rune {
		if r == '/' || r == '\\' || r == 0 {
			return -1
		}
		return r
	}, name)
	return name
}

func freeBytes(path string) (int64, error) {
	if err := os.MkdirAll(path, 0755); err != nil {
		return 0, err
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, err
	}
	return int64(stat.Bavail) * int64(stat.Bsize), nil
}

type activityReader struct {
	r        io.Reader
	interval time.Duration
	last     time.Time
	touch    func()
}

func (r *activityReader) Read(p []byte) (int, error) {
	n, err := r.r.Read(p)
	if n > 0 && (r.last.IsZero() || time.Since(r.last) >= r.interval) {
		r.last = time.Now()
		r.touch()
	}
	return n, err
}
