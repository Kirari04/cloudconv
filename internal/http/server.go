package http

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	stdhttp "net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"

	"github.com/kirari04/cloudconv/internal/auth"
	"github.com/kirari04/cloudconv/internal/config"
	"github.com/kirari04/cloudconv/internal/jobs"
	"github.com/kirari04/cloudconv/internal/media"
	"github.com/kirari04/cloudconv/internal/store"
	"github.com/kirari04/cloudconv/internal/uploads"
)

type Server struct {
	cfg        config.Config
	store      *store.Store
	auth       *auth.Service
	uploads    *uploads.Service
	jobs       *jobs.Service
	legacyJobs map[string]legacyJob
}

type legacyJob struct {
	JobID string
	Token string
}

type contextKey string

const sessionKey contextKey = "session"

func New(cfg config.Config, st *store.Store, authSvc *auth.Service, uploadSvc *uploads.Service, jobSvc *jobs.Service) *Server {
	return &Server{
		cfg:        cfg,
		store:      st,
		auth:       authSvc,
		uploads:    uploadSvc,
		jobs:       jobSvc,
		legacyJobs: make(map[string]legacyJob),
	}
}

func (s *Server) Routes() stdhttp.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.RealIP)
	r.Use(middleware.Recoverer)
	r.Use(s.sessionMiddleware)
	r.Use(s.csrfMiddleware)

	r.Get("/api/config", s.configHandler)
	r.Post("/api/setup", s.setupHandler)
	r.Post("/api/auth/login", s.loginHandler)
	r.Post("/api/auth/logout", s.logoutHandler)
	r.Get("/api/auth/session", s.sessionHandler)

	r.Post("/api/uploads", s.initiateUploadHandler)
	r.Get("/api/uploads/{uploadId}", s.uploadStatusHandler)
	r.Put("/api/uploads/{uploadId}/chunks/{chunkIndex}", s.chunkUploadHandler)
	r.Post("/api/uploads/{uploadId}/complete", s.completeUploadHandler)
	r.Get("/api/jobs/{jobId}", s.jobStatusHandler)
	r.Post("/api/jobs/{jobId}/cancel", s.cancelJobHandler)
	r.Get("/download/{jobId}", s.downloadHandler)

	r.Post("/api/uploads/initiate", s.legacyInitiateHandler)
	r.Post("/api/uploads/{uploadId}", s.legacyUploadHandler)
	r.Get("/api/uploads/{uploadId}/status", s.legacyStatusHandler)

	r.Route("/api/admin", func(r chi.Router) {
		r.Use(s.adminOnly)
		r.Get("/summary", s.adminSummaryHandler)
		r.Get("/jobs", s.adminJobsHandler)
		r.Get("/uploads", s.adminUploadsHandler)
		r.Get("/events", s.adminEventsHandler)
		r.Get("/settings", s.adminSettingsHandler)
		r.Patch("/settings", s.adminPatchSettingsHandler)
		r.Get("/users", s.adminUsersHandler)
		r.Post("/users", s.adminCreateUserHandler)
		r.Patch("/users/{userId}", s.adminPatchUserHandler)
		r.Post("/users/{userId}/reset-password", s.adminResetPasswordHandler)
		r.Delete("/users/{userId}", s.adminDeleteUserHandler)
	})

	r.Get("/audio", redirectHome)
	r.Get("/image", redirectHome)
	r.Get("/*", s.spaHandler)
	return r
}

func redirectHome(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	stdhttp.Redirect(w, r, "/", stdhttp.StatusFound)
}

func (s *Server) sessionMiddleware(next stdhttp.Handler) stdhttp.Handler {
	return stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		current, _ := s.auth.Current(r.Context(), r)
		ctx := context.WithValue(r.Context(), sessionKey, current)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) csrfMiddleware(next stdhttp.Handler) stdhttp.Handler {
	return stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		if r.Method == stdhttp.MethodGet || r.Method == stdhttp.MethodHead || r.Method == stdhttp.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		if r.URL.Path == "/api/auth/login" || r.URL.Path == "/api/setup" {
			next.ServeHTTP(w, r)
			return
		}
		current := currentSession(r)
		if current.User != nil {
			if r.Header.Get("X-CSRF-Token") != current.CSRF {
				writeError(w, stdhttp.StatusForbidden, "invalid CSRF token")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) adminOnly(next stdhttp.Handler) stdhttp.Handler {
	return stdhttp.HandlerFunc(func(w stdhttp.ResponseWriter, r *stdhttp.Request) {
		current := currentSession(r)
		if current.User == nil || current.User.Role != "admin" {
			writeError(w, stdhttp.StatusForbidden, "admin access required")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func currentSession(r *stdhttp.Request) *auth.SessionUser {
	current, ok := r.Context().Value(sessionKey).(*auth.SessionUser)
	if !ok || current == nil {
		return &auth.SessionUser{}
	}
	return current
}

func (s *Server) configHandler(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	settings, err := s.store.Settings(r.Context())
	if err != nil {
		writeError(w, 500, "could not load settings")
		return
	}
	setupNeeded, _ := s.auth.SetupNeeded(r.Context())
	public := map[string]string{
		"public_uploads_enabled": settings["public_uploads_enabled"],
		"max_upload_bytes":       settings["max_upload_bytes"],
		"chunk_size_bytes":       settings["chunk_size_bytes"],
	}
	writeJSON(w, 200, map[string]any{
		"catalog":     media.DefaultCatalog(),
		"settings":    public,
		"setupNeeded": setupNeeded,
		"auth":        currentSession(r),
	})
}

func (s *Server) setupHandler(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	needed, err := s.auth.SetupNeeded(r.Context())
	if err != nil {
		writeError(w, 500, "could not check setup state")
		return
	}
	if !needed {
		writeError(w, 409, "setup has already been completed")
		return
	}
	var body struct {
		Email      string `json:"email"`
		Password   string `json:"password"`
		SetupToken string `json:"setupToken"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if body.SetupToken == "" {
		body.SetupToken = r.Header.Get("X-Setup-Token")
	}
	if body.SetupToken != s.cfg.SetupToken {
		writeError(w, 403, "invalid setup token")
		return
	}
	user, err := s.auth.CreateUser(r.Context(), body.Email, body.Password, "admin")
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	_ = s.store.AddEvent(r.Context(), store.Event{Level: "info", Kind: "setup.completed", ActorUserID: &user.ID, Message: "first admin created", CreatedAt: time.Now().UTC()})
	writeJSON(w, 201, map[string]any{"user": user})
}

func (s *Server) loginHandler(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	session, err := s.auth.Login(r.Context(), w, r, body.Email, body.Password)
	if err != nil {
		writeError(w, 401, err.Error())
		return
	}
	_ = s.store.AddEvent(r.Context(), store.Event{Level: "info", Kind: "auth.login", ActorUserID: &session.User.ID, Message: "user logged in", CreatedAt: time.Now().UTC()})
	writeJSON(w, 200, session)
}

func (s *Server) logoutHandler(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	_ = s.auth.Logout(r.Context(), w, r)
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func (s *Server) sessionHandler(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	writeJSON(w, 200, currentSession(r))
}

func (s *Server) initiateUploadHandler(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	var body uploads.InitiateRequest
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	current := currentSession(r)
	resp, err := s.uploads.Initiate(r.Context(), body, current.User, auth.ClientIP(r, s.cfg.TrustProxy), r.UserAgent())
	if err != nil {
		writeError(w, statusForError(err), err.Error())
		return
	}
	writeJSON(w, 201, resp)
}

func (s *Server) chunkUploadHandler(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	uploadID := chi.URLParam(r, "uploadId")
	index, err := strconv.Atoi(chi.URLParam(r, "chunkIndex"))
	if err != nil {
		writeError(w, 400, "invalid chunk index")
		return
	}
	current := currentSession(r)
	err = s.uploads.SaveChunk(r.Context(), uploadID, index, r.Body, r.ContentLength, r.Header.Get("Content-Range"), r.Header.Get("X-Chunk-SHA256"), current.User, uploads.TokenFromRequest(r))
	if err != nil {
		drainChunkBody(r)
		writeError(w, statusForError(err), err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"status": "ok"})
}

func (s *Server) uploadStatusHandler(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	current := currentSession(r)
	upload, missing, err := s.uploads.Status(r.Context(), chi.URLParam(r, "uploadId"), current.User, uploads.TokenFromRequest(r))
	if err != nil {
		writeError(w, statusForError(err), err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"upload": upload, "missingChunks": missing})
}

func (s *Server) completeUploadHandler(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	var body uploads.CompleteRequest
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	current := currentSession(r)
	token := uploads.TokenFromRequest(r)
	upload, _, err := s.uploads.Assemble(r.Context(), chi.URLParam(r, "uploadId"), current.User, token)
	if err != nil {
		writeError(w, statusForError(err), err.Error())
		return
	}
	job, _, err := s.jobs.Create(r.Context(), upload, body.TargetFormat, body.Preset, body.Options)
	if err != nil {
		writeError(w, statusForError(err), err.Error())
		return
	}
	writeJSON(w, 201, map[string]any{"job": s.jobs.JobResponse(r.Context(), job, token)})
}

func (s *Server) jobStatusHandler(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	job, token, ok := s.authorizeJobRequest(w, r)
	if !ok {
		return
	}
	writeJSON(w, 200, s.jobs.JobResponse(r.Context(), job, token))
}

func (s *Server) cancelJobHandler(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	job, _, ok := s.authorizeJobRequest(w, r)
	if !ok {
		return
	}
	if err := s.jobs.Cancel(r.Context(), job.ID); err != nil {
		writeError(w, 500, "could not cancel job")
		return
	}
	writeJSON(w, 200, map[string]string{"status": "canceled"})
}

func (s *Server) downloadHandler(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	job, _, ok := s.authorizeJobRequest(w, r)
	if !ok {
		return
	}
	if job.Status != "finished" || job.OutputPath == nil {
		writeError(w, 404, "download is not available")
		return
	}
	upload, err := s.store.UploadByID(r.Context(), job.UploadID)
	if err != nil {
		writeError(w, 404, "upload not found")
		return
	}
	downloadName := strings.TrimSuffix(filepath.Base(upload.OriginalFilename), filepath.Ext(upload.OriginalFilename)) + "." + media.ExtensionFor(job.TargetFormat)
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", sanitizeDownloadName(downloadName)))
	stdhttp.ServeFile(w, r, *job.OutputPath)
}

func (s *Server) authorizeJobRequest(w stdhttp.ResponseWriter, r *stdhttp.Request) (*store.Job, string, bool) {
	job, err := s.store.JobByID(r.Context(), chi.URLParam(r, "jobId"))
	if err != nil {
		writeError(w, 404, "job not found")
		return nil, "", false
	}
	current := currentSession(r)
	token := uploads.TokenFromRequest(r)
	if job.OwnerUserID != nil {
		if current.User == nil {
			writeError(w, 401, "login is required")
			return nil, "", false
		}
		if current.User.Role != "admin" && current.User.ID != *job.OwnerUserID {
			writeError(w, 403, "job is not accessible")
			return nil, "", false
		}
		return job, token, true
	}
	if job.AnonymousTokenHash != nil {
		if token == "" {
			writeError(w, 401, "job token is required")
			return nil, "", false
		}
		if auth.HashToken(token) != *job.AnonymousTokenHash {
			writeError(w, 403, "invalid job token")
			return nil, "", false
		}
	}
	return job, token, true
}

func (s *Server) legacyInitiateHandler(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	id := uuid.NewString()
	s.legacyJobs[id] = legacyJob{}
	writeJSON(w, 201, map[string]string{"uploadId": id})
}

func (s *Server) legacyUploadHandler(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	legacyID := chi.URLParam(r, "uploadId")
	if _, ok := s.legacyJobs[legacyID]; !ok {
		writeError(w, 404, "invalid upload session")
		return
	}
	maxUpload, _ := s.store.SettingInt64(r.Context(), "max_upload_bytes")
	r.Body = stdhttp.MaxBytesReader(w, r.Body, maxUpload)
	if err := r.ParseMultipartForm(32 << 20); err != nil {
		writeError(w, 400, "could not parse multipart form")
		return
	}
	formValues := make(map[string]string)
	for key, values := range r.MultipartForm.Value {
		if len(values) > 0 {
			formValues[key] = values[0]
		}
	}
	format, preset, opts, err := media.LegacyOptions(formValues)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	file, header, err := legacyFile(r)
	if err != nil {
		writeError(w, 400, "could not retrieve file")
		return
	}
	defer file.Close()
	plain, hash := auth.NewAnonymousToken()
	tokenHash := &hash
	legacyDir := filepath.Join(s.cfg.UploadDir, "legacy", legacyID)
	if err := os.MkdirAll(legacyDir, 0755); err != nil {
		writeError(w, 500, "could not prepare upload directory")
		return
	}
	sourcePath := filepath.Join(legacyDir, "source"+filepath.Ext(header.Filename))
	dst, err := os.Create(sourcePath)
	if err != nil {
		writeError(w, 500, "could not save file")
		return
	}
	size, copyErr := io.Copy(dst, file)
	closeErr := dst.Close()
	if copyErr != nil || closeErr != nil {
		writeError(w, 500, "could not save file")
		return
	}
	info, err := media.Probe(r.Context(), sourcePath)
	if err != nil {
		writeError(w, 400, "unsupported media file")
		return
	}
	if err := media.Validate(info.MediaType, format, preset, opts); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	upload, err := s.uploads.CreateCompletedUpload(r.Context(), header.Filename, sourcePath, size, nil, tokenHash, auth.ClientIP(r, s.cfg.TrustProxy), r.UserAgent(), info)
	if err != nil {
		writeError(w, 500, "could not create upload")
		return
	}
	job, _, err := s.jobs.Create(r.Context(), upload, format, preset, opts)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	s.legacyJobs[legacyID] = legacyJob{JobID: job.ID, Token: plain}
	writeJSON(w, 200, map[string]string{"message": "Upload complete, conversion is queued."})
}

func (s *Server) legacyStatusHandler(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	legacy, ok := s.legacyJobs[chi.URLParam(r, "uploadId")]
	if !ok || legacy.JobID == "" {
		writeError(w, 404, "job not found")
		return
	}
	job, err := s.store.JobByID(r.Context(), legacy.JobID)
	if err != nil {
		writeError(w, 404, "job not found")
		return
	}
	resp := s.jobs.JobResponse(r.Context(), job, legacy.Token)
	resp["originalFilename"] = ""
	resp["targetFormat"] = job.TargetFormat
	writeJSON(w, 200, resp)
}

func legacyFile(r *stdhttp.Request) (multipart.File, *multipart.FileHeader, error) {
	for _, field := range []string{"imageFile", "videoFile", "audioFile"} {
		file, header, err := r.FormFile(field)
		if err == nil {
			return file, header, nil
		}
	}
	return nil, nil, errors.New("missing file")
}

func (s *Server) adminSummaryHandler(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	summary, err := s.store.Summary(r.Context())
	if err != nil {
		writeError(w, 500, "could not load summary")
		return
	}
	summary["disk"] = diskSummary(s.cfg.UploadDir, s.cfg.ConvertedDir)
	writeJSON(w, 200, summary)
}

func (s *Server) adminJobsHandler(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	limit := queryLimit(r, 100)
	jobs, err := s.store.ListJobs(r.Context(), limit)
	if err != nil {
		writeError(w, 500, "could not load jobs")
		return
	}
	writeJSON(w, 200, map[string]any{"jobs": jobs})
}

func (s *Server) adminUploadsHandler(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	limit := queryLimit(r, 100)
	uploads, err := s.store.ListUploads(r.Context(), limit)
	if err != nil {
		writeError(w, 500, "could not load uploads")
		return
	}
	writeJSON(w, 200, map[string]any{"uploads": uploads})
}

func (s *Server) adminEventsHandler(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	limit := queryLimit(r, 200)
	events, err := s.store.ListEvents(r.Context(), limit)
	if err != nil {
		writeError(w, 500, "could not load events")
		return
	}
	writeJSON(w, 200, map[string]any{"events": events})
}

func (s *Server) adminSettingsHandler(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	settings, err := s.store.Settings(r.Context())
	if err != nil {
		writeError(w, 500, "could not load settings")
		return
	}
	writeJSON(w, 200, map[string]any{"settings": settings})
}

func (s *Server) adminPatchSettingsHandler(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	var body struct {
		Settings map[string]string `json:"settings"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if err := s.store.UpdateSettings(r.Context(), body.Settings); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	s.adminSettingsHandler(w, r)
}

func (s *Server) adminUsersHandler(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	users, err := s.store.ListUsers(r.Context())
	if err != nil {
		writeError(w, 500, "could not load users")
		return
	}
	writeJSON(w, 200, map[string]any{"users": users})
}

func (s *Server) adminCreateUserHandler(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
		Role     string `json:"role"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	user, err := s.auth.CreateUser(r.Context(), body.Email, body.Password, body.Role)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 201, map[string]any{"user": user})
}

func (s *Server) adminPatchUserHandler(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	var body struct {
		Email    *string `json:"email"`
		Role     *string `json:"role"`
		Disabled *bool   `json:"disabled"`
	}
	if err := readJSON(r, &body); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	fields := map[string]any{}
	if body.Email != nil {
		fields["email"] = *body.Email
	}
	if body.Role != nil {
		fields["role"] = *body.Role
	}
	if body.Disabled != nil {
		fields["disabled"] = *body.Disabled
	}
	if err := s.store.UpdateUser(r.Context(), chi.URLParam(r, "userId"), fields); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	s.adminUsersHandler(w, r)
}

func (s *Server) adminResetPasswordHandler(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	var body struct {
		Password string `json:"password"`
	}
	_ = readJSON(r, &body)
	if body.Password == "" {
		body.Password = uuid.NewString()[:12] + "Aa1!"
	}
	hash, err := auth.HashPassword(body.Password)
	if err != nil {
		writeError(w, 400, err.Error())
		return
	}
	if err := s.store.UpdateUser(r.Context(), chi.URLParam(r, "userId"), map[string]any{"password_hash": hash}); err != nil {
		writeError(w, 400, err.Error())
		return
	}
	writeJSON(w, 200, map[string]string{"password": body.Password})
}

func (s *Server) adminDeleteUserHandler(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	if err := s.store.DeleteUser(r.Context(), chi.URLParam(r, "userId")); err != nil {
		writeError(w, 500, "could not delete user")
		return
	}
	writeJSON(w, 200, map[string]string{"status": "deleted"})
}

func (s *Server) spaHandler(w stdhttp.ResponseWriter, r *stdhttp.Request) {
	path := filepath.Join("web", "dist", strings.TrimPrefix(filepath.Clean(r.URL.Path), "/"))
	if info, err := os.Stat(path); err == nil && !info.IsDir() {
		stdhttp.ServeFile(w, r, path)
		return
	}
	indexPath := filepath.Join("web", "dist", "index.html")
	if _, err := os.Stat(indexPath); err == nil {
		stdhttp.ServeFile(w, r, indexPath)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(200)
	_, _ = w.Write([]byte(`<div id="app">CloudConv frontend has not been built. Run npm install && npm run build.</div>`))
}

func readJSON(r *stdhttp.Request, dest any) error {
	defer r.Body.Close()
	decoder := json.NewDecoder(io.LimitReader(r.Body, 2<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(dest); err != nil {
		return err
	}
	return nil
}

func writeJSON(w stdhttp.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func writeError(w stdhttp.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func drainChunkBody(r *stdhttp.Request) {
	if r.Body == nil {
		return
	}
	const maxDrain = 64 << 20
	_, _ = io.Copy(io.Discard, io.LimitReader(r.Body, maxDrain))
}

func statusForError(err error) int {
	if err == nil {
		return 200
	}
	msg := strings.ToLower(err.Error())
	switch {
	case strings.Contains(msg, "login is required") || strings.Contains(msg, "token is required"):
		return 401
	case strings.Contains(msg, "not accessible") || strings.Contains(msg, "invalid upload token") || strings.Contains(msg, "invalid job token"):
		return 403
	case errors.Is(err, sql.ErrNoRows):
		return 404
	default:
		return 400
	}
}

func queryLimit(r *stdhttp.Request, fallback int) int {
	limit, err := strconv.Atoi(r.URL.Query().Get("limit"))
	if err != nil || limit <= 0 || limit > 1000 {
		return fallback
	}
	return limit
}

func sanitizeDownloadName(name string) string {
	name = filepath.Base(name)
	name = strings.ReplaceAll(name, "\x00", "")
	if strings.TrimSpace(name) == "" {
		return "converted-file"
	}
	return name
}

func diskSummary(paths ...string) map[string]any {
	out := map[string]any{}
	for _, path := range paths {
		if err := os.MkdirAll(path, 0755); err != nil {
			continue
		}
		var size int64
		_ = filepath.WalkDir(path, func(_ string, d os.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if info, err := d.Info(); err == nil {
				size += info.Size()
			}
			return nil
		})
		out[path] = size
	}
	return out
}
