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
	"github.com/kirari04/cloudconv/internal/store"
	"github.com/kirari04/cloudconv/internal/uploads"
)

type testApp struct {
	server *httptest.Server
	client *http.Client
	cancel context.CancelFunc
	store  *store.Store
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
