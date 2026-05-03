package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kirari04/cloudconv/internal/auth"
	"github.com/kirari04/cloudconv/internal/config"
	apphttp "github.com/kirari04/cloudconv/internal/http"
	"github.com/kirari04/cloudconv/internal/jobs"
	"github.com/kirari04/cloudconv/internal/media"
	"github.com/kirari04/cloudconv/internal/store"
	"github.com/kirari04/cloudconv/internal/uploads"
)

type testApp struct {
	server *httptest.Server
	client *http.Client
	cancel context.CancelFunc
	store  *store.Store
	cfg    config.Config
}

func newTestApp(t *testing.T) *testApp {
	t.Helper()
	root := t.TempDir()
	cfg := config.Config{
		Addr:         ":0",
		DBPath:       filepath.Join(root, "cloudconv.db"),
		UploadDir:    filepath.Join(root, "uploads"),
		ConvertedDir: filepath.Join(root, "converted"),
		SetupToken:   "test-setup-token",
	}
	ctx, cancel := context.WithCancel(context.Background())
	st, err := store.Open(ctx, cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := st.UpdateSettings(ctx, map[string]string{
		"chunk_size_bytes":                  "1024",
		"min_free_disk_bytes":               "0",
		"max_upload_starts_per_ip_per_hour": "100",
		"max_jobs_per_ip_per_day":           "100",
	}); err != nil {
		t.Fatalf("update settings: %v", err)
	}
	authSvc := auth.New(st, false)
	uploadSvc := uploads.New(cfg, st)
	jobSvc := jobs.New(cfg, st)
	jobSvc.Start(ctx)
	srv := apphttp.New(cfg, st, authSvc, uploadSvc, jobSvc)
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	app := &testApp{
		server: httptest.NewServer(srv.Routes()),
		client: &http.Client{Jar: jar},
		cancel: cancel,
		store:  st,
		cfg:    cfg,
	}
	t.Cleanup(func() {
		app.server.Close()
		cancel()
		st.Close()
	})
	return app
}

func TestSetupLoginSettingsAndPublicAccessGate(t *testing.T) {
	app := newTestApp(t)

	resp := app.doJSON(t, "POST", "/api/setup", map[string]any{
		"email":      "admin@example.com",
		"password":   "password123",
		"setupToken": "wrong",
	}, "")
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected wrong setup token to be rejected, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp = app.doJSON(t, "POST", "/api/setup", map[string]any{
		"email":      "admin@example.com",
		"password":   "password123",
		"setupToken": "test-setup-token",
	}, "")
	assertStatus(t, resp, http.StatusCreated)
	resp.Body.Close()

	login := app.decodeJSON(t, app.doJSON(t, "POST", "/api/auth/login", map[string]any{
		"email":    "admin@example.com",
		"password": "password123",
	}, ""))
	csrf := login["csrfToken"].(string)
	if csrf == "" {
		t.Fatal("expected csrf token")
	}

	resp = app.doJSON(t, "PATCH", "/api/admin/settings", map[string]any{
		"settings": map[string]string{
			"public_uploads_enabled": "false",
			"max_upload_bytes":       "2048",
		},
	}, csrf)
	assertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	anonymous := &http.Client{}
	body := bytes.NewBufferString(`{"filename":"x.png","size":4,"mime":"image/png"}`)
	req, _ := http.NewRequest("POST", app.server.URL+"/api/uploads", body)
	req.Header.Set("Content-Type", "application/json")
	resp, err := anonymous.Do(req)
	if err != nil {
		t.Fatalf("anonymous upload request: %v", err)
	}
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected anonymous upload to require login, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp = app.doJSON(t, "POST", "/api/uploads", map[string]any{
		"filename": "x.png",
		"size":     4,
		"mime":     "image/png",
	}, csrf)
	assertStatus(t, resp, http.StatusCreated)
	resp.Body.Close()
}

func TestInactiveUploadSessionsDoNotBlockActiveLimit(t *testing.T) {
	app := newTestApp(t)
	ctx := context.Background()
	if err := app.store.UpdateSettings(ctx, map[string]string{
		"max_active_uploads_per_ip":         "1",
		"upload_inactivity_timeout_minutes": "1",
	}); err != nil {
		t.Fatalf("update settings: %v", err)
	}

	first := app.decodeJSON(t, app.doJSON(t, "POST", "/api/uploads", map[string]any{
		"filename": "abandoned.png",
		"size":     1024,
		"mime":     "image/png",
	}, ""))
	firstUploadID := first["uploadId"].(string)
	staleTime := time.Now().UTC().Add(-2 * time.Minute).Format(time.RFC3339Nano)
	if _, err := app.store.DB().ExecContext(ctx, `UPDATE uploads SET updated_at = ? WHERE id = ?`, staleTime, firstUploadID); err != nil {
		t.Fatalf("make upload stale: %v", err)
	}

	resp := app.doJSON(t, "POST", "/api/uploads", map[string]any{
		"filename": "next.png",
		"size":     1024,
		"mime":     "image/png",
	}, "")
	assertStatus(t, resp, http.StatusCreated)
	resp.Body.Close()

	staleUpload, err := app.store.UploadByID(ctx, firstUploadID)
	if err != nil {
		t.Fatalf("load stale upload: %v", err)
	}
	if staleUpload.Status != "canceled" {
		t.Fatalf("expected stale upload to be canceled, got %s", staleUpload.Status)
	}

	resp = app.doJSON(t, "POST", "/api/uploads", map[string]any{
		"filename": "blocked.png",
		"size":     1024,
		"mime":     "image/png",
	}, "")
	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected active upload limit to still block fresh sessions, got %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	resp.Body.Close()
}

func TestAuthenticatedChunkUploadUsesCSRF(t *testing.T) {
	app := newTestApp(t)
	resp := app.doJSON(t, "POST", "/api/setup", map[string]any{
		"email":      "admin@example.com",
		"password":   "password123",
		"setupToken": "test-setup-token",
	}, "")
	assertStatus(t, resp, http.StatusCreated)
	resp.Body.Close()
	login := app.decodeJSON(t, app.doJSON(t, "POST", "/api/auth/login", map[string]any{
		"email":    "admin@example.com",
		"password": "password123",
	}, ""))
	csrf := login["csrfToken"].(string)

	init := app.decodeJSON(t, app.doJSON(t, "POST", "/api/uploads", map[string]any{
		"filename": "auth.bin",
		"size":     4,
		"mime":     "application/octet-stream",
	}, csrf))
	uploadID := init["uploadId"].(string)

	req, _ := http.NewRequest("PUT", app.server.URL+"/api/uploads/"+uploadID+"/chunks/0", bytes.NewReader([]byte("test")))
	req.Header.Set("Content-Range", "bytes 0-3/4")
	resp, err := app.client.Do(req)
	if err != nil {
		t.Fatalf("chunk without csrf: %v", err)
	}
	if resp.StatusCode != http.StatusForbidden {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected missing csrf to be rejected, got %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	resp.Body.Close()

	req, _ = http.NewRequest("PUT", app.server.URL+"/api/uploads/"+uploadID+"/chunks/0", bytes.NewReader([]byte("test")))
	req.Header.Set("Content-Range", "bytes 0-3/4")
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err = app.client.Do(req)
	if err != nil {
		t.Fatalf("chunk with csrf: %v", err)
	}
	assertStatus(t, resp, http.StatusOK)
	resp.Body.Close()
}

func TestConfigExposesAvailableContainerCodecs(t *testing.T) {
	tests := []struct {
		name       string
		encoders   map[string]bool
		wantAV1    bool
		wantAV1Enc string
		wantCount  int
	}{
		{
			name: "svt av1 preferred",
			encoders: map[string]bool{
				"libx264":    true,
				"libx265":    true,
				"libsvtav1":  true,
				"librav1e":   true,
				"libaom-av1": true,
				"libvpx-vp9": true,
				"aac":        true,
				"libopus":    true,
			},
			wantAV1:    true,
			wantAV1Enc: "libsvtav1",
			wantCount:  3,
		},
		{
			name: "aom av1 fallback",
			encoders: map[string]bool{
				"libx264":    true,
				"libaom-av1": true,
				"aac":        true,
			},
			wantAV1:    true,
			wantAV1Enc: "libaom-av1",
			wantCount:  2,
		},
		{
			name: "av1 hidden when unavailable",
			encoders: map[string]bool{
				"libx264": true,
				"libx265": true,
				"aac":     true,
			},
			wantAV1:   false,
			wantCount: 2,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			restore := media.SetAvailableEncodersForTest(tt.encoders)
			defer restore()
			app := newTestApp(t)

			config := app.decodeJSON(t, app.doJSON(t, "GET", "/api/config", nil, ""))
			catalog := config["catalog"].(map[string]any)
			presetDetails := catalog["presetDetails"].([]any)
			if len(presetDetails) != 3 {
				t.Fatalf("expected presetDetails for three presets, got %#v", presetDetails)
			}
			balanced := findPresetDetail(t, presetDetails, "balanced")
			effects := balanced["effects"].(map[string]any)
			videoEffect := effects["video"].(map[string]any)
			values := videoEffect["values"].(map[string]any)
			if values["maxHeight"] != float64(720) {
				t.Fatalf("expected balanced video maxHeight 720, got %#v", values)
			}
			formats := catalog["formats"].([]any)
			mp4 := findFormat(t, formats, "mp4")
			videoCodecs := mp4["videoCodecs"].([]any)
			if len(videoCodecs) != tt.wantCount {
				t.Fatalf("expected %d mp4 codecs, got %#v", tt.wantCount, videoCodecs)
			}
			av1 := findCodec(videoCodecs, "av1")
			if tt.wantAV1 {
				if av1 == nil {
					t.Fatalf("expected AV1 codec in %#v", videoCodecs)
				}
				if av1["encoder"] != tt.wantAV1Enc {
					t.Fatalf("expected AV1 encoder %s, got %#v", tt.wantAV1Enc, av1)
				}
			} else if av1 != nil {
				t.Fatalf("expected AV1 to be omitted, got %#v", av1)
			}
			gif := findFormat(t, formats, "gif")
			if _, ok := gif["videoCodecs"]; ok {
				t.Fatalf("expected gif to omit videoCodecs, got %#v", gif["videoCodecs"])
			}
			mp3 := findFormat(t, formats, "mp3")
			if _, ok := mp3["audioCodecs"]; ok {
				t.Fatalf("expected audio-only format to omit audioCodecs, got %#v", mp3["audioCodecs"])
			}
		})
	}
}

func TestJobCreateStoresEffectiveAV1Encoder(t *testing.T) {
	restore := media.SetAvailableEncodersForTest(map[string]bool{
		"libsvtav1":  true,
		"librav1e":   true,
		"libaom-av1": true,
		"aac":        true,
	})
	defer restore()

	ctx := context.Background()
	root := t.TempDir()
	cfg := config.Config{
		DBPath:       filepath.Join(root, "cloudconv.db"),
		UploadDir:    filepath.Join(root, "uploads"),
		ConvertedDir: filepath.Join(root, "converted"),
	}
	st, err := store.Open(ctx, cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	sourcePath := filepath.Join(cfg.UploadDir, "av1-upload", "source.mp4")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0755); err != nil {
		t.Fatalf("mkdir source: %v", err)
	}
	if err := os.WriteFile(sourcePath, []byte("source"), 0644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	mediaType := "video"
	now := time.Now().UTC()
	upload := store.Upload{
		ID:               "av1-upload",
		OriginalFilename: "clip.mp4",
		SourcePath:       &sourcePath,
		MediaType:        &mediaType,
		SizeBytes:        6,
		BytesReceived:    6,
		ChunkSizeBytes:   1024,
		ChunkCount:       1,
		Status:           "complete",
		IPAddress:        "127.0.0.1",
		UserAgent:        "test",
		CreatedAt:        now,
		UpdatedAt:        now,
		ExpiresAt:        now.Add(time.Hour),
	}
	if err := st.CreateUpload(ctx, upload); err != nil {
		t.Fatalf("create upload: %v", err)
	}

	jobSvc := jobs.New(cfg, st)
	job, _, err := jobSvc.Create(ctx, &upload, "mp4", "balanced", media.Options{
		VideoCodec:            "av1",
		AudioCodec:            "aac",
		EffectiveVideoEncoder: "libaom-av1",
		EffectiveAudioEncoder: "spoofed",
	})
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	stored, err := st.JobByID(ctx, job.ID)
	if err != nil {
		t.Fatalf("load job: %v", err)
	}
	opts := media.DecodeOptions(stored.OptionsJSON)
	if opts.VideoCodec != "av1" || opts.AudioCodec != "aac" {
		t.Fatalf("expected stored codec ids, got %#v", opts)
	}
	if opts.EffectiveVideoEncoder != "libsvtav1" {
		t.Fatalf("expected effective AV1 encoder libsvtav1, got %#v", opts)
	}
	if opts.EffectiveAudioEncoder != "aac" {
		t.Fatalf("expected effective audio encoder aac, got %#v", opts)
	}
}

func TestChunkedUploadConversionAndDownload(t *testing.T) {
	app := newTestApp(t)
	data, err := os.ReadFile("testdata/test5.png")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	initResp := app.decodeJSON(t, app.doJSON(t, "POST", "/api/uploads", map[string]any{
		"filename": "test5.png",
		"size":     len(data),
		"mime":     "image/png",
	}, ""))
	uploadID := initResp["uploadId"].(string)
	token := initResp["token"].(string)
	chunkSize := int(initResp["chunkSizeBytes"].(float64))
	chunkCount := int(initResp["chunkCount"].(float64))
	for i := 0; i < chunkCount; i++ {
		start := i * chunkSize
		end := start + chunkSize
		if end > len(data) {
			end = len(data)
		}
		req, _ := http.NewRequest("PUT", app.server.URL+"/api/uploads/"+uploadID+"/chunks/"+itoa(i), bytes.NewReader(data[start:end]))
		req.Header.Set("Content-Range", "bytes "+itoa(start)+"-"+itoa(end-1)+"/"+itoa(len(data)))
		req.Header.Set("X-CloudConv-Token", token)
		resp, err := app.client.Do(req)
		if err != nil {
			t.Fatalf("upload chunk: %v", err)
		}
		assertStatus(t, resp, http.StatusOK)
		resp.Body.Close()
	}
	complete := app.decodeJSON(t, app.doJSON(t, "POST", "/api/uploads/"+uploadID+"/complete?token="+token, map[string]any{
		"targetFormat": "jpg",
		"preset":       "balanced",
		"options":      map[string]any{"maxWidth": 128},
	}, ""))
	job := complete["job"].(map[string]any)
	jobID := job["id"].(string)
	final := pollJob(t, app.client, app.server.URL, jobID, token)
	if final["status"] != "finished" {
		t.Fatalf("expected finished job, got %#v", final)
	}
	downloadURL := final["downloadUrl"].(string)
	resp, err := app.client.Get(app.server.URL + downloadURL)
	if err != nil {
		t.Fatalf("download: %v", err)
	}
	assertStatus(t, resp, http.StatusOK)
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if len(body) == 0 {
		t.Fatal("download body was empty")
	}
}

func TestLegacyMultipartEndpointStillQueuesJob(t *testing.T) {
	app := newTestApp(t)
	init := app.decodeJSON(t, app.doJSON(t, "POST", "/api/uploads/initiate", map[string]any{}, ""))
	legacyID := init["uploadId"].(string)
	req := multipartRequest(t, app.server.URL+"/api/uploads/"+legacyID, "testdata/test5.png", "imageFile", map[string]string{"format": "jpg", "resolution": "128"})
	resp, err := app.client.Do(req)
	if err != nil {
		t.Fatalf("legacy upload: %v", err)
	}
	assertStatus(t, resp, http.StatusOK)
	resp.Body.Close()
	final := pollLegacy(t, app.client, app.server.URL, legacyID)
	if final["status"] != "finished" {
		t.Fatalf("expected finished legacy job, got %#v", final)
	}
}

func TestAdminCanCancelUploadAndArtifacts(t *testing.T) {
	app := newTestApp(t)
	csrf, adminID := setupAdmin(t, app)
	ctx := context.Background()
	now := time.Now().UTC()
	uploadID := "upload-cancel-test"
	sourcePath := filepath.Join(app.cfg.UploadDir, uploadID, "source.png")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0755); err != nil {
		t.Fatalf("mkdir source: %v", err)
	}
	if err := os.WriteFile(sourcePath, []byte("source"), 0644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	chunkDir := filepath.Join(app.cfg.UploadDir, uploadID, "chunks")
	if err := os.MkdirAll(chunkDir, 0755); err != nil {
		t.Fatalf("mkdir chunks: %v", err)
	}
	chunkPath := filepath.Join(chunkDir, "000000.part")
	if err := os.WriteFile(chunkPath, []byte("chunk"), 0644); err != nil {
		t.Fatalf("write chunk: %v", err)
	}
	upload := store.Upload{
		ID:               uploadID,
		OriginalFilename: "cancel-me.png",
		SourcePath:       &sourcePath,
		SizeBytes:        6,
		BytesReceived:    5,
		ChunkSizeBytes:   1024,
		ChunkCount:       1,
		Status:           "uploading",
		IPAddress:        "127.0.0.1",
		UserAgent:        "test",
		CreatedAt:        now,
		UpdatedAt:        now,
		ExpiresAt:        now.Add(time.Hour),
	}
	if err := app.store.CreateUpload(ctx, upload); err != nil {
		t.Fatalf("create upload: %v", err)
	}
	job := store.Job{
		ID:           "queued-cancel-test",
		UploadID:     uploadID,
		Status:       "queued",
		TargetFormat: "jpg",
		Preset:       "balanced",
		OptionsJSON:  "{}",
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := app.store.CreateJob(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	resp := app.doJSON(t, "POST", "/api/admin/uploads/"+uploadID+"/cancel", map[string]any{"note": "cleanup"}, csrf)
	assertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	updated, err := app.store.UploadByID(ctx, uploadID)
	if err != nil {
		t.Fatalf("load upload: %v", err)
	}
	if updated.Status != "canceled" || updated.CanceledByUserID == nil || *updated.CanceledByUserID != adminID {
		t.Fatalf("expected upload canceled by admin, got %#v", updated)
	}
	updatedJob, err := app.store.JobByID(ctx, job.ID)
	if err != nil {
		t.Fatalf("load job: %v", err)
	}
	if updatedJob.Status != "canceled" {
		t.Fatalf("expected related queued job canceled, got %s", updatedJob.Status)
	}
	if _, err := os.Stat(sourcePath); !os.IsNotExist(err) {
		t.Fatalf("expected source artifact removed, stat err=%v", err)
	}
	if _, err := os.Stat(chunkDir); !os.IsNotExist(err) {
		t.Fatalf("expected chunk directory removed, stat err=%v", err)
	}
	events, total, err := app.store.ListEventsFiltered(ctx, store.AdminEventFilter{Kind: "upload.canceled", Limit: 10})
	if err != nil {
		t.Fatalf("list events: %v", err)
	}
	if total == 0 || len(events) == 0 {
		t.Fatal("expected upload.canceled event")
	}
}

func TestAdminRemoveJobDeletesArtifactAndHidesByDefault(t *testing.T) {
	app := newTestApp(t)
	csrf, adminID := setupAdmin(t, app)
	ctx := context.Background()
	now := time.Now().UTC()
	sourcePath := filepath.Join(app.cfg.UploadDir, "remove-upload-test", "source.png")
	if err := os.MkdirAll(filepath.Dir(sourcePath), 0755); err != nil {
		t.Fatalf("mkdir source: %v", err)
	}
	if err := os.WriteFile(sourcePath, []byte("source"), 0644); err != nil {
		t.Fatalf("write source: %v", err)
	}
	upload := store.Upload{
		ID:               "remove-upload-test",
		OriginalFilename: "remove-me.png",
		SourcePath:       &sourcePath,
		SizeBytes:        6,
		BytesReceived:    6,
		ChunkSizeBytes:   1024,
		ChunkCount:       1,
		Status:           "complete",
		IPAddress:        "127.0.0.1",
		UserAgent:        "test",
		CreatedAt:        now,
		UpdatedAt:        now,
		ExpiresAt:        now.Add(time.Hour),
	}
	if err := app.store.CreateUpload(ctx, upload); err != nil {
		t.Fatalf("create upload: %v", err)
	}
	if err := os.MkdirAll(app.cfg.ConvertedDir, 0755); err != nil {
		t.Fatalf("mkdir converted: %v", err)
	}
	outputPath := filepath.Join(app.cfg.ConvertedDir, "remove-job-test.jpg")
	if err := os.WriteFile(outputPath, []byte("output"), 0644); err != nil {
		t.Fatalf("write output: %v", err)
	}
	outputSize := int64(6)
	finished := now.Add(time.Minute)
	job := store.Job{
		ID:              "remove-job-test",
		UploadID:        upload.ID,
		Status:          "finished",
		TargetFormat:    "jpg",
		Preset:          "balanced",
		OptionsJSON:     "{}",
		OutputPath:      &outputPath,
		OutputSizeBytes: &outputSize,
		FinishedAt:      &finished,
		CreatedAt:       now,
		UpdatedAt:       finished,
	}
	if err := app.store.CreateJob(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	resp := app.doJSON(t, "DELETE", "/api/admin/jobs/"+job.ID, map[string]any{"note": "remove artifact"}, csrf)
	assertStatus(t, resp, http.StatusOK)
	resp.Body.Close()

	updated, err := app.store.JobByID(ctx, job.ID)
	if err != nil {
		t.Fatalf("load job: %v", err)
	}
	if updated.Status != "removed" || updated.RemovedByUserID == nil || *updated.RemovedByUserID != adminID {
		t.Fatalf("expected removed job by admin, got %#v", updated)
	}
	if _, err := os.Stat(outputPath); !os.IsNotExist(err) {
		t.Fatalf("expected output artifact removed, stat err=%v", err)
	}
	defaultList := app.decodeJSON(t, app.doJSON(t, "GET", "/api/admin/jobs?limit=50", nil, csrf))
	if len(defaultList["jobs"].([]any)) != 0 {
		t.Fatalf("expected removed job hidden by default, got %#v", defaultList["jobs"])
	}
	removedList := app.decodeJSON(t, app.doJSON(t, "GET", "/api/admin/jobs?includeRemoved=true&limit=50", nil, csrf))
	if len(removedList["jobs"].([]any)) == 0 {
		t.Fatal("expected removed job visible with includeRemoved")
	}
}

func TestAdminUserSelfLockoutProtections(t *testing.T) {
	app := newTestApp(t)
	csrf, adminID := setupAdmin(t, app)

	resp := app.doJSON(t, "PATCH", "/api/admin/users/"+adminID, map[string]any{"disabled": true}, csrf)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected self-disable rejected, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp = app.doJSON(t, "PATCH", "/api/admin/users/"+adminID, map[string]any{"role": "user"}, csrf)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected self-demote rejected, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	resp = app.doJSON(t, "DELETE", "/api/admin/users/"+adminID, map[string]any{}, csrf)
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("expected self-delete rejected, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	created := app.decodeJSON(t, app.doJSON(t, "POST", "/api/admin/users", map[string]any{
		"email":    "user@example.com",
		"password": "temporary123",
		"role":     "user",
	}, csrf))
	user := created["user"].(map[string]any)
	reset := app.decodeJSON(t, app.doJSON(t, "POST", "/api/admin/users/"+user["id"].(string)+"/reset-password", map[string]any{}, csrf))
	password := reset["password"].(string)
	resp = app.doJSON(t, "POST", "/api/auth/login", map[string]any{
		"email":    "user@example.com",
		"password": password,
	}, "")
	assertStatus(t, resp, http.StatusOK)
	resp.Body.Close()
}

func TestPublicJobStatusHidesConverterDetails(t *testing.T) {
	app := newTestApp(t)
	csrf, _ := setupAdmin(t, app)
	ctx := context.Background()
	now := time.Now().UTC()
	upload := store.Upload{
		ID:               "failed-upload-test",
		OriginalFilename: "bad-video.mp4",
		SizeBytes:        10,
		BytesReceived:    10,
		ChunkSizeBytes:   10,
		ChunkCount:       1,
		Status:           "complete",
		IPAddress:        "127.0.0.1",
		UserAgent:        "test",
		CreatedAt:        now,
		UpdatedAt:        now,
		ExpiresAt:        now.Add(time.Hour),
	}
	if err := app.store.CreateUpload(ctx, upload); err != nil {
		t.Fatalf("create upload: %v", err)
	}
	details := "conversion failed: ffmpeg exited with exit code 1: private codec details"
	job := store.Job{
		ID:           "failed-job-test",
		UploadID:     upload.ID,
		Status:       "error",
		TargetFormat: "mp4",
		Preset:       "balanced",
		OptionsJSON:  "{}",
		ErrorMessage: &details,
		FinishedAt:   &now,
		CreatedAt:    now,
		UpdatedAt:    now,
	}
	if err := app.store.CreateJob(ctx, job); err != nil {
		t.Fatalf("create job: %v", err)
	}

	public := app.decodeJSON(t, app.doJSON(t, "GET", "/api/jobs/"+job.ID, nil, csrf))
	if public["error"] != "Conversion failed." {
		t.Fatalf("expected sanitized public error, got %#v", public["error"])
	}
	admin := app.decodeJSON(t, app.doJSON(t, "GET", "/api/admin/jobs?status=error&includeRemoved=true", nil, csrf))
	jobs := admin["jobs"].([]any)
	if len(jobs) != 1 {
		t.Fatalf("expected one admin job, got %#v", jobs)
	}
	raw := jobs[0].(map[string]any)["error"]
	if raw != details {
		t.Fatalf("expected raw admin error, got %#v", raw)
	}
}

func setupAdmin(t *testing.T, app *testApp) (string, string) {
	t.Helper()
	resp := app.doJSON(t, "POST", "/api/setup", map[string]any{
		"email":      "admin@example.com",
		"password":   "password123",
		"setupToken": "test-setup-token",
	}, "")
	assertStatus(t, resp, http.StatusCreated)
	resp.Body.Close()
	login := app.decodeJSON(t, app.doJSON(t, "POST", "/api/auth/login", map[string]any{
		"email":    "admin@example.com",
		"password": "password123",
	}, ""))
	user := login["user"].(map[string]any)
	return login["csrfToken"].(string), user["id"].(string)
}

func findFormat(t *testing.T, formats []any, id string) map[string]any {
	t.Helper()
	for _, item := range formats {
		format := item.(map[string]any)
		if format["id"] == id {
			return format
		}
	}
	t.Fatalf("format %s not found in %#v", id, formats)
	return nil
}

func findPresetDetail(t *testing.T, details []any, id string) map[string]any {
	t.Helper()
	for _, item := range details {
		detail := item.(map[string]any)
		if detail["id"] == id {
			return detail
		}
	}
	t.Fatalf("preset detail %s not found in %#v", id, details)
	return nil
}

func findCodec(codecs []any, id string) map[string]any {
	for _, item := range codecs {
		codec := item.(map[string]any)
		if codec["id"] == id {
			return codec
		}
	}
	return nil
}

func (app *testApp) doJSON(t *testing.T, method, path string, payload any, csrf string) *http.Response {
	t.Helper()
	var body io.Reader
	if payload != nil {
		data, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal json: %v", err)
		}
		body = bytes.NewReader(data)
	}
	req, err := http.NewRequest(method, app.server.URL+path, body)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	resp, err := app.client.Do(req)
	if err != nil {
		t.Fatalf("request %s %s: %v", method, path, err)
	}
	return resp
}

func (app *testApp) decodeJSON(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	assertStatus(t, resp, resp.StatusCode)
	var payload map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		t.Fatalf("decode json: %v", err)
	}
	resp.Body.Close()
	return payload
}

func assertStatus(t *testing.T, resp *http.Response, expected int) {
	t.Helper()
	if resp.StatusCode != expected {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("expected status %d, got %d: %s", expected, resp.StatusCode, strings.TrimSpace(string(body)))
	}
}

func pollJob(t *testing.T, client *http.Client, baseURL, jobID, token string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(baseURL + "/api/jobs/" + jobID + "?token=" + token)
		if err != nil {
			t.Fatalf("poll job: %v", err)
		}
		var payload map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			t.Fatalf("decode poll: %v", err)
		}
		resp.Body.Close()
		if payload["status"] == "finished" || payload["status"] == "error" {
			return payload
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatal("timed out polling job")
	return nil
}

func pollLegacy(t *testing.T, client *http.Client, baseURL, uploadID string) map[string]any {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get(baseURL + "/api/uploads/" + uploadID + "/status")
		if err != nil {
			t.Fatalf("poll legacy: %v", err)
		}
		var payload map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
			t.Fatalf("decode poll: %v", err)
		}
		resp.Body.Close()
		if payload["status"] == "finished" || payload["status"] == "error" {
			return payload
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatal("timed out polling legacy job")
	return nil
}

func multipartRequest(t *testing.T, url, filePath, field string, fields map[string]string) *http.Request {
	t.Helper()
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	file, err := os.Open(filePath)
	if err != nil {
		t.Fatalf("open fixture: %v", err)
	}
	defer file.Close()
	part, err := writer.CreateFormFile(field, filepath.Base(filePath))
	if err != nil {
		t.Fatalf("form file: %v", err)
	}
	if _, err := io.Copy(part, file); err != nil {
		t.Fatalf("copy fixture: %v", err)
	}
	for key, value := range fields {
		if err := writer.WriteField(key, value); err != nil {
			t.Fatalf("write field: %v", err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	req, err := http.NewRequest("POST", url, body)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

func itoa(v int) string {
	return strconv.Itoa(v)
}
