package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

const timeFormat = time.RFC3339Nano

var DefaultSettings = map[string]string{
	"public_uploads_enabled":            "true",
	"max_upload_bytes":                  "10737418240",
	"chunk_size_bytes":                  "16777216",
	"max_queue_depth":                   "100",
	"max_active_uploads_per_ip":         "2",
	"max_upload_starts_per_ip_per_hour": "10",
	"max_jobs_per_ip_per_day":           "25",
	"max_concurrent_jobs":               "1",
	"conversion_timeout_minutes":        "240",
	"upload_inactivity_timeout_minutes": "30",
	"finished_file_retention_hours":     "24",
	"failed_upload_retention_hours":     "24",
	"event_retention_days":              "30",
	"min_free_disk_bytes":               "21474836480",
}

type Store struct {
	db *sql.DB
}

var ErrTerminalState = errors.New("record is already terminal")

const uploadSelectColumns = `id, owner_user_id, anonymous_token_hash, original_filename, source_path, media_type, detected_mime, size_bytes, bytes_received, chunk_size_bytes, chunk_count, status, ip_address, user_agent, created_at, updated_at, expires_at, canceled_at, canceled_by_user_id, artifacts_deleted_at, artifact_error, admin_note`

const jobSelectColumns = `id, upload_id, owner_user_id, anonymous_token_hash, status, target_format, preset, options_json, progress_percentage, output_path, output_size_bytes, error_message, started_at, finished_at, created_at, updated_at, removed_at, removed_by_user_id, artifacts_deleted_at, artifact_error, admin_note`

type AdminJobFilter struct {
	Limit          int
	Offset         int
	Status         string
	TargetFormat   string
	UploadID       string
	UserID         string
	Query          string
	IncludeRemoved bool
}

type AdminUploadFilter struct {
	Limit     int
	Offset    int
	Status    string
	MediaType string
	UserID    string
	Query     string
}

type AdminUserFilter struct {
	Limit    int
	Offset   int
	Role     string
	Disabled *bool
	Query    string
}

type AdminEventFilter struct {
	Limit    int
	Offset   int
	Level    string
	Kind     string
	JobID    string
	UploadID string
	UserID   string
	Query    string
}

func Open(ctx context.Context, path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, "PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON; PRAGMA busy_timeout=5000;"); err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{db: db}
	if err := s.Migrate(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) DB() *sql.DB {
	return s.db
}

func (s *Store) Migrate(ctx context.Context) error {
	statements := []string{
		`CREATE TABLE IF NOT EXISTS settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS users (
			id TEXT PRIMARY KEY,
			email TEXT UNIQUE NOT NULL,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL CHECK(role IN ('admin','user')),
			disabled INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			last_login_at TEXT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			token_hash TEXT UNIQUE NOT NULL,
			user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			csrf_token TEXT NOT NULL,
			expires_at TEXT NOT NULL,
			created_at TEXT NOT NULL,
			ip_address TEXT,
			user_agent TEXT
		);`,
		`CREATE TABLE IF NOT EXISTS uploads (
			id TEXT PRIMARY KEY,
			owner_user_id TEXT NULL REFERENCES users(id) ON DELETE SET NULL,
			anonymous_token_hash TEXT NULL,
			original_filename TEXT NOT NULL,
			source_path TEXT NULL,
			media_type TEXT NULL,
			detected_mime TEXT NULL,
			size_bytes INTEGER NOT NULL,
			bytes_received INTEGER NOT NULL DEFAULT 0,
			chunk_size_bytes INTEGER NOT NULL,
			chunk_count INTEGER NOT NULL,
			status TEXT NOT NULL,
			ip_address TEXT,
			user_agent TEXT,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL,
			expires_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS upload_chunks (
			upload_id TEXT NOT NULL REFERENCES uploads(id) ON DELETE CASCADE,
			chunk_index INTEGER NOT NULL,
			size_bytes INTEGER NOT NULL,
			sha256 TEXT NULL,
			path TEXT NOT NULL,
			received_at TEXT NOT NULL,
			PRIMARY KEY (upload_id, chunk_index)
		);`,
		`CREATE TABLE IF NOT EXISTS jobs (
			id TEXT PRIMARY KEY,
			upload_id TEXT NOT NULL REFERENCES uploads(id) ON DELETE CASCADE,
			owner_user_id TEXT NULL REFERENCES users(id) ON DELETE SET NULL,
			anonymous_token_hash TEXT NULL,
			status TEXT NOT NULL,
			target_format TEXT NOT NULL,
			preset TEXT NOT NULL,
			options_json TEXT NOT NULL,
			progress_percentage INTEGER NOT NULL DEFAULT 0,
			output_path TEXT NULL,
			output_size_bytes INTEGER NULL,
			error_message TEXT NULL,
			started_at TEXT NULL,
			finished_at TEXT NULL,
			created_at TEXT NOT NULL,
			updated_at TEXT NOT NULL
		);`,
		`CREATE TABLE IF NOT EXISTS events (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			level TEXT NOT NULL,
			kind TEXT NOT NULL,
			actor_user_id TEXT NULL,
			upload_id TEXT NULL,
			job_id TEXT NULL,
			message TEXT NOT NULL,
			metadata_json TEXT NULL,
			ip_address TEXT NULL,
			user_agent TEXT NULL,
			created_at TEXT NOT NULL
		);`,
		`CREATE INDEX IF NOT EXISTS idx_uploads_ip_created ON uploads(ip_address, created_at);`,
		`CREATE INDEX IF NOT EXISTS idx_jobs_status_created ON jobs(status, created_at);`,
		`CREATE INDEX IF NOT EXISTS idx_events_created ON events(created_at);`,
		`CREATE INDEX IF NOT EXISTS idx_sessions_token ON sessions(token_hash);`,
	}
	for _, stmt := range statements {
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	for _, column := range []struct {
		table      string
		name       string
		definition string
	}{
		{"jobs", "removed_at", "removed_at TEXT NULL"},
		{"jobs", "removed_by_user_id", "removed_by_user_id TEXT NULL"},
		{"jobs", "artifacts_deleted_at", "artifacts_deleted_at TEXT NULL"},
		{"jobs", "artifact_error", "artifact_error TEXT NULL"},
		{"jobs", "admin_note", "admin_note TEXT NULL"},
		{"uploads", "canceled_at", "canceled_at TEXT NULL"},
		{"uploads", "canceled_by_user_id", "canceled_by_user_id TEXT NULL"},
		{"uploads", "artifacts_deleted_at", "artifacts_deleted_at TEXT NULL"},
		{"uploads", "artifact_error", "artifact_error TEXT NULL"},
		{"uploads", "admin_note", "admin_note TEXT NULL"},
	} {
		if err := s.addColumnIfMissing(ctx, column.table, column.name, column.definition); err != nil {
			return err
		}
	}
	now := nowString()
	for key, value := range DefaultSettings {
		if _, err := s.db.ExecContext(ctx, `INSERT OR IGNORE INTO settings(key, value, updated_at) VALUES(?, ?, ?)`, key, value, now); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) addColumnIfMissing(ctx context.Context, table, column, definition string) error {
	rows, err := s.db.QueryContext(ctx, `PRAGMA table_info(`+table+`)`)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var cid int
		var name, dataType string
		var notNull int
		var defaultValue any
		var pk int
		if err := rows.Scan(&cid, &name, &dataType, &notNull, &defaultValue, &pk); err != nil {
			return err
		}
		if name == column {
			return nil
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `ALTER TABLE `+table+` ADD COLUMN `+definition)
	return err
}

func (s *Store) Settings(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT key, value FROM settings ORDER BY key`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]string)
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		out[key] = value
	}
	return out, rows.Err()
}

func (s *Store) Setting(ctx context.Context, key string) (string, error) {
	var value string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&value)
	return value, err
}

func (s *Store) SettingBool(ctx context.Context, key string) (bool, error) {
	value, err := s.Setting(ctx, key)
	if err != nil {
		return false, err
	}
	return strconv.ParseBool(value)
}

func (s *Store) SettingInt64(ctx context.Context, key string) (int64, error) {
	value, err := s.Setting(ctx, key)
	if err != nil {
		return 0, err
	}
	return strconv.ParseInt(value, 10, 64)
}

func (s *Store) UpdateSettings(ctx context.Context, settings map[string]string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	now := nowString()
	for key, value := range settings {
		if _, ok := DefaultSettings[key]; !ok {
			return fmt.Errorf("unknown setting: %s", key)
		}
		if _, err := tx.ExecContext(ctx, `UPDATE settings SET value = ?, updated_at = ? WHERE key = ?`, value, now, key); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *Store) HasAdmin(ctx context.Context) (bool, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE role = 'admin'`).Scan(&count)
	return count > 0, err
}

func (s *Store) CreateUser(ctx context.Context, u User) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO users(id, email, password_hash, role, disabled, created_at, updated_at, last_login_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
		u.ID, strings.ToLower(u.Email), u.PasswordHash, u.Role, boolInt(u.Disabled), formatTime(u.CreatedAt), formatTime(u.UpdatedAt), nullableTime(u.LastLoginAt))
	return err
}

func (s *Store) UserByEmail(ctx context.Context, email string) (*User, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, email, password_hash, role, disabled, created_at, updated_at, last_login_at FROM users WHERE email = ?`, strings.ToLower(email))
	return scanUser(row)
}

func (s *Store) UserByID(ctx context.Context, id string) (*User, error) {
	row := s.db.QueryRowContext(ctx, `SELECT id, email, password_hash, role, disabled, created_at, updated_at, last_login_at FROM users WHERE id = ?`, id)
	return scanUser(row)
}

func (s *Store) ListUsers(ctx context.Context) ([]User, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, email, password_hash, role, disabled, created_at, updated_at, last_login_at FROM users ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	users := make([]User, 0)
	for rows.Next() {
		u, err := scanUserRows(rows)
		if err != nil {
			return nil, err
		}
		users = append(users, *u)
	}
	return users, rows.Err()
}

func (s *Store) ListUsersFiltered(ctx context.Context, filter AdminUserFilter) ([]User, int, error) {
	limit, offset := normalizeLimitOffset(filter.Limit, filter.Offset)
	where := []string{"1=1"}
	args := make([]any, 0)
	if filter.Role != "" {
		where = append(where, "role = ?")
		args = append(args, filter.Role)
	}
	if filter.Disabled != nil {
		where = append(where, "disabled = ?")
		args = append(args, boolInt(*filter.Disabled))
	}
	if strings.TrimSpace(filter.Query) != "" {
		q := "%" + strings.ToLower(strings.TrimSpace(filter.Query)) + "%"
		where = append(where, "(LOWER(email) LIKE ? OR LOWER(id) LIKE ?)")
		args = append(args, q, q)
	}
	whereSQL := strings.Join(where, " AND ")
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE `+whereSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	queryArgs := append(append([]any{}, args...), limit, offset)
	rows, err := s.db.QueryContext(ctx, `SELECT id, email, password_hash, role, disabled, created_at, updated_at, last_login_at FROM users WHERE `+whereSQL+` ORDER BY created_at DESC LIMIT ? OFFSET ?`, queryArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	users := make([]User, 0)
	for rows.Next() {
		u, err := scanUserRows(rows)
		if err != nil {
			return nil, 0, err
		}
		users = append(users, *u)
	}
	return users, total, rows.Err()
}

func (s *Store) UpdateUser(ctx context.Context, id string, fields map[string]any) error {
	if len(fields) == 0 {
		return nil
	}
	sets := make([]string, 0, len(fields)+1)
	args := make([]any, 0, len(fields)+2)
	for key, value := range fields {
		switch key {
		case "email":
			sets = append(sets, "email = ?")
			args = append(args, strings.ToLower(fmt.Sprint(value)))
		case "password_hash":
			sets = append(sets, "password_hash = ?")
			args = append(args, value)
		case "role":
			sets = append(sets, "role = ?")
			args = append(args, value)
		case "disabled":
			sets = append(sets, "disabled = ?")
			args = append(args, boolInt(value.(bool)))
		default:
			return fmt.Errorf("unknown user field: %s", key)
		}
	}
	sets = append(sets, "updated_at = ?")
	args = append(args, nowString(), id)
	_, err := s.db.ExecContext(ctx, `UPDATE users SET `+strings.Join(sets, ", ")+` WHERE id = ?`, args...)
	return err
}

func (s *Store) DeleteUser(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id = ?`, id)
	return err
}

func (s *Store) ActiveEnabledAdminCount(ctx context.Context) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM users WHERE role = 'admin' AND disabled = 0`).Scan(&count)
	return count, err
}

func (s *Store) MarkLogin(ctx context.Context, id string) error {
	now := nowString()
	_, err := s.db.ExecContext(ctx, `UPDATE users SET last_login_at = ?, updated_at = ? WHERE id = ?`, now, now, id)
	return err
}

func (s *Store) CreateSession(ctx context.Context, session Session) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO sessions(id, token_hash, user_id, csrf_token, expires_at, created_at, ip_address, user_agent) VALUES(?, ?, ?, ?, ?, ?, ?, ?)`,
		session.ID, session.TokenHash, session.UserID, session.CSRFToken, formatTime(session.ExpiresAt), formatTime(session.CreatedAt), session.IPAddress, session.UserAgent)
	return err
}

func (s *Store) SessionByTokenHash(ctx context.Context, tokenHash string) (*Session, *User, error) {
	row := s.db.QueryRowContext(ctx, `SELECT s.id, s.token_hash, s.user_id, s.csrf_token, s.expires_at, s.created_at, s.ip_address, s.user_agent,
		u.id, u.email, u.password_hash, u.role, u.disabled, u.created_at, u.updated_at, u.last_login_at
		FROM sessions s JOIN users u ON u.id = s.user_id WHERE s.token_hash = ? AND s.expires_at > ?`, tokenHash, nowString())
	var sess Session
	var user User
	var sessExpires, sessCreated string
	var userCreated, userUpdated string
	var lastLogin sql.NullString
	if err := row.Scan(&sess.ID, &sess.TokenHash, &sess.UserID, &sess.CSRFToken, &sessExpires, &sessCreated, &sess.IPAddress, &sess.UserAgent,
		&user.ID, &user.Email, &user.PasswordHash, &user.Role, &user.Disabled, &userCreated, &userUpdated, &lastLogin); err != nil {
		return nil, nil, err
	}
	sess.ExpiresAt = parseTime(sessExpires)
	sess.CreatedAt = parseTime(sessCreated)
	user.CreatedAt = parseTime(userCreated)
	user.UpdatedAt = parseTime(userUpdated)
	user.LastLoginAt = parseNullableTime(lastLogin)
	return &sess, &user, nil
}

func (s *Store) DeleteSession(ctx context.Context, tokenHash string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE token_hash = ?`, tokenHash)
	return err
}

func (s *Store) CleanupSessions(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at <= ?`, nowString())
	return err
}

func (s *Store) CreateUpload(ctx context.Context, upload Upload) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO uploads(id, owner_user_id, anonymous_token_hash, original_filename, source_path, media_type, detected_mime, size_bytes, bytes_received, chunk_size_bytes, chunk_count, status, ip_address, user_agent, created_at, updated_at, expires_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		upload.ID, nullableStringPtr(upload.OwnerUserID), nullableStringPtr(upload.AnonymousTokenHash), upload.OriginalFilename, nullableStringPtr(upload.SourcePath), nullableStringPtr(upload.MediaType), nullableStringPtr(upload.DetectedMIME),
		upload.SizeBytes, upload.BytesReceived, upload.ChunkSizeBytes, upload.ChunkCount, upload.Status, upload.IPAddress, upload.UserAgent, formatTime(upload.CreatedAt), formatTime(upload.UpdatedAt), formatTime(upload.ExpiresAt))
	return err
}

func (s *Store) UploadByID(ctx context.Context, id string) (*Upload, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+uploadSelectColumns+` FROM uploads WHERE id = ?`, id)
	return scanUpload(row)
}

func (s *Store) ListUploads(ctx context.Context, limit int) ([]Upload, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+uploadSelectColumns+` FROM uploads ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Upload, 0)
	for rows.Next() {
		u, err := scanUploadRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *u)
	}
	return out, rows.Err()
}

func (s *Store) ListUploadsFiltered(ctx context.Context, filter AdminUploadFilter) ([]Upload, int, error) {
	limit, offset := normalizeLimitOffset(filter.Limit, filter.Offset)
	where := []string{"1=1"}
	args := make([]any, 0)
	if filter.Status != "" {
		where = append(where, "status = ?")
		args = append(args, filter.Status)
	}
	if filter.MediaType != "" {
		where = append(where, "media_type = ?")
		args = append(args, filter.MediaType)
	}
	if filter.UserID != "" {
		where = append(where, "owner_user_id = ?")
		args = append(args, filter.UserID)
	}
	if strings.TrimSpace(filter.Query) != "" {
		q := "%" + strings.ToLower(strings.TrimSpace(filter.Query)) + "%"
		where = append(where, "(LOWER(id) LIKE ? OR LOWER(original_filename) LIKE ? OR LOWER(COALESCE(ip_address, '')) LIKE ?)")
		args = append(args, q, q, q)
	}
	whereSQL := strings.Join(where, " AND ")
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM uploads WHERE `+whereSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	queryArgs := append(append([]any{}, args...), limit, offset)
	rows, err := s.db.QueryContext(ctx, `SELECT `+uploadSelectColumns+` FROM uploads WHERE `+whereSQL+` ORDER BY created_at DESC LIMIT ? OFFSET ?`, queryArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]Upload, 0)
	for rows.Next() {
		u, err := scanUploadRows(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *u)
	}
	return out, total, rows.Err()
}

func (s *Store) UpdateUploadChunk(ctx context.Context, uploadID string, chunk UploadChunk) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `INSERT INTO upload_chunks(upload_id, chunk_index, size_bytes, sha256, path, received_at)
		VALUES(?, ?, ?, ?, ?, ?)
		ON CONFLICT(upload_id, chunk_index) DO UPDATE SET size_bytes = excluded.size_bytes, sha256 = excluded.sha256, path = excluded.path, received_at = excluded.received_at`,
		uploadID, chunk.Index, chunk.SizeBytes, nullableStringPtr(chunk.SHA256), chunk.Path, formatTime(chunk.ReceivedAt))
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE uploads SET bytes_received = COALESCE((SELECT SUM(size_bytes) FROM upload_chunks WHERE upload_id = ?), 0), updated_at = ? WHERE id = ?`, uploadID, nowString(), uploadID)
	if err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) MissingChunks(ctx context.Context, uploadID string, chunkCount int) ([]int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT chunk_index FROM upload_chunks WHERE upload_id = ? ORDER BY chunk_index`, uploadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	seen := make(map[int]bool)
	for rows.Next() {
		var idx int
		if err := rows.Scan(&idx); err != nil {
			return nil, err
		}
		seen[idx] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	missing := make([]int, 0)
	for i := 0; i < chunkCount; i++ {
		if !seen[i] {
			missing = append(missing, i)
		}
	}
	return missing, nil
}

func (s *Store) Chunks(ctx context.Context, uploadID string) ([]UploadChunk, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT upload_id, chunk_index, size_bytes, sha256, path, received_at FROM upload_chunks WHERE upload_id = ? ORDER BY chunk_index`, uploadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	chunks := make([]UploadChunk, 0)
	for rows.Next() {
		var chunk UploadChunk
		var sha sql.NullString
		var received string
		if err := rows.Scan(&chunk.UploadID, &chunk.Index, &chunk.SizeBytes, &sha, &chunk.Path, &received); err != nil {
			return nil, err
		}
		chunk.SHA256 = nullablePtr(sha)
		chunk.ReceivedAt = parseTime(received)
		chunks = append(chunks, chunk)
	}
	return chunks, rows.Err()
}

func (s *Store) CompleteUpload(ctx context.Context, uploadID, sourcePath, mediaType, detectedMIME string) error {
	now := nowString()
	_, err := s.db.ExecContext(ctx, `UPDATE uploads SET source_path = ?, media_type = ?, detected_mime = ?, status = 'complete', updated_at = ? WHERE id = ?`, sourcePath, mediaType, detectedMIME, now, uploadID)
	return err
}

func (s *Store) UpdateUploadStatus(ctx context.Context, uploadID, status string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE uploads SET status = ?, updated_at = ? WHERE id = ?`, status, nowString(), uploadID)
	return err
}

func (s *Store) TouchUpload(ctx context.Context, uploadID string) error {
	_, err := s.db.ExecContext(ctx, `UPDATE uploads SET updated_at = ? WHERE id = ? AND status = 'uploading'`, nowString(), uploadID)
	return err
}

func (s *Store) CreateJob(ctx context.Context, job Job) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO jobs(id, upload_id, owner_user_id, anonymous_token_hash, status, target_format, preset, options_json, progress_percentage, output_path, output_size_bytes, error_message, started_at, finished_at, created_at, updated_at)
		VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		job.ID, job.UploadID, nullableStringPtr(job.OwnerUserID), nullableStringPtr(job.AnonymousTokenHash), job.Status, job.TargetFormat, job.Preset, job.OptionsJSON, job.ProgressPercentage,
		nullableStringPtr(job.OutputPath), nullableInt64Ptr(job.OutputSizeBytes), nullableStringPtr(job.ErrorMessage), nullableTime(job.StartedAt), nullableTime(job.FinishedAt), formatTime(job.CreatedAt), formatTime(job.UpdatedAt))
	return err
}

func (s *Store) JobByID(ctx context.Context, id string) (*Job, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+jobSelectColumns+` FROM jobs WHERE id = ?`, id)
	return scanJob(row)
}

func (s *Store) ListJobs(ctx context.Context, limit int) ([]Job, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+jobSelectColumns+` FROM jobs ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Job, 0)
	for rows.Next() {
		j, err := scanJobRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *j)
	}
	return out, rows.Err()
}

func (s *Store) ListJobsFiltered(ctx context.Context, filter AdminJobFilter) ([]Job, int, error) {
	limit, offset := normalizeLimitOffset(filter.Limit, filter.Offset)
	where := []string{"1=1"}
	args := make([]any, 0)
	if !filter.IncludeRemoved {
		where = append(where, "j.status <> 'removed'")
	}
	if filter.Status != "" {
		where = append(where, "j.status = ?")
		args = append(args, filter.Status)
	}
	if filter.TargetFormat != "" {
		where = append(where, "j.target_format = ?")
		args = append(args, filter.TargetFormat)
	}
	if filter.UploadID != "" {
		where = append(where, "j.upload_id = ?")
		args = append(args, filter.UploadID)
	}
	if filter.UserID != "" {
		where = append(where, "j.owner_user_id = ?")
		args = append(args, filter.UserID)
	}
	if strings.TrimSpace(filter.Query) != "" {
		q := "%" + strings.ToLower(strings.TrimSpace(filter.Query)) + "%"
		where = append(where, "(LOWER(j.id) LIKE ? OR LOWER(j.upload_id) LIKE ? OR LOWER(COALESCE(u.original_filename, '')) LIKE ?)")
		args = append(args, q, q, q)
	}
	whereSQL := strings.Join(where, " AND ")
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs j LEFT JOIN uploads u ON u.id = j.upload_id WHERE `+whereSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	queryArgs := append(append([]any{}, args...), limit, offset)
	rows, err := s.db.QueryContext(ctx, `SELECT `+prefixColumns("j", jobSelectColumns)+` FROM jobs j LEFT JOIN uploads u ON u.id = j.upload_id WHERE `+whereSQL+` ORDER BY j.created_at DESC LIMIT ? OFFSET ?`, queryArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]Job, 0)
	for rows.Next() {
		j, err := scanJobRows(rows)
		if err != nil {
			return nil, 0, err
		}
		out = append(out, *j)
	}
	return out, total, rows.Err()
}

func (s *Store) JobsByUploadID(ctx context.Context, uploadID string) ([]Job, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+jobSelectColumns+` FROM jobs WHERE upload_id = ? ORDER BY created_at DESC`, uploadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Job, 0)
	for rows.Next() {
		j, err := scanJobRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *j)
	}
	return out, rows.Err()
}

func (s *Store) NonRemovedActiveJobsByUploadID(ctx context.Context, uploadID string) ([]Job, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+jobSelectColumns+` FROM jobs WHERE upload_id = ? AND status IN ('queued','converting') ORDER BY created_at DESC`, uploadID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Job, 0)
	for rows.Next() {
		j, err := scanJobRows(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *j)
	}
	return out, rows.Err()
}

func (s *Store) ClaimNextJob(ctx context.Context) (*Job, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	row := tx.QueryRowContext(ctx, `SELECT id FROM jobs WHERE status = 'queued' ORDER BY created_at LIMIT 1`)
	var id string
	if err := row.Scan(&id); err != nil {
		return nil, err
	}
	now := nowString()
	result, err := tx.ExecContext(ctx, `UPDATE jobs SET status = 'converting', started_at = ?, updated_at = ? WHERE id = ? AND status = 'queued'`, now, now, id)
	if err != nil {
		return nil, err
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return nil, errors.New("job already claimed")
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return s.JobByID(ctx, id)
}

func (s *Store) UpdateJobProgress(ctx context.Context, id string, progress int) error {
	_, err := s.db.ExecContext(ctx, `UPDATE jobs SET progress_percentage = ?, updated_at = ? WHERE id = ? AND status = 'converting'`, progress, nowString(), id)
	return err
}

func (s *Store) FinishJob(ctx context.Context, id, outputPath string, size int64) error {
	now := nowString()
	_, err := s.db.ExecContext(ctx, `UPDATE jobs SET status = 'finished', progress_percentage = 100, output_path = ?, output_size_bytes = ?, finished_at = ?, updated_at = ? WHERE id = ? AND status = 'converting'`, outputPath, size, now, now, id)
	return err
}

func (s *Store) FailJob(ctx context.Context, id, message string) error {
	now := nowString()
	_, err := s.db.ExecContext(ctx, `UPDATE jobs SET status = 'error', error_message = ?, finished_at = ?, updated_at = ? WHERE id = ? AND status IN ('queued','converting')`, message, now, now, id)
	return err
}

func (s *Store) CancelJob(ctx context.Context, id string) error {
	now := nowString()
	result, err := s.db.ExecContext(ctx, `UPDATE jobs SET status = 'canceled', finished_at = ?, updated_at = ? WHERE id = ? AND status IN ('queued','converting')`, now, now, id)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 1 {
		return nil
	}
	var status string
	if err := s.db.QueryRowContext(ctx, `SELECT status FROM jobs WHERE id = ?`, id).Scan(&status); err != nil {
		return err
	}
	return ErrTerminalState
}

func (s *Store) CancelJobForAdmin(ctx context.Context, jobID, adminID, note string, artifactError *string) (*Job, error) {
	now := nowString()
	result, err := s.db.ExecContext(ctx, `UPDATE jobs SET status = 'canceled', finished_at = ?, updated_at = ?, artifact_error = ?, admin_note = ? WHERE id = ? AND status IN ('queued','converting')`,
		now, now, nullableStringPtr(artifactError), nullableNote(note), jobID)
	if err != nil {
		return nil, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if affected != 1 {
		var status string
		if err := s.db.QueryRowContext(ctx, `SELECT status FROM jobs WHERE id = ?`, jobID).Scan(&status); err != nil {
			return nil, err
		}
		return nil, ErrTerminalState
	}
	return s.JobByID(ctx, jobID)
}

func (s *Store) MarkJobRemoved(ctx context.Context, jobID, adminID, note string, artifactError *string) (*Job, error) {
	now := nowString()
	var deletedAt any
	if artifactError == nil {
		deletedAt = now
	}
	result, err := s.db.ExecContext(ctx, `UPDATE jobs SET status = 'removed', removed_at = ?, removed_by_user_id = ?, artifacts_deleted_at = ?, artifact_error = ?, admin_note = ?, finished_at = COALESCE(finished_at, ?), updated_at = ? WHERE id = ? AND status <> 'removed'`,
		now, adminID, deletedAt, nullableStringPtr(artifactError), nullableNote(note), now, now, jobID)
	if err != nil {
		return nil, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if affected != 1 {
		var status string
		if err := s.db.QueryRowContext(ctx, `SELECT status FROM jobs WHERE id = ?`, jobID).Scan(&status); err != nil {
			return nil, err
		}
		return nil, ErrTerminalState
	}
	return s.JobByID(ctx, jobID)
}

func (s *Store) CancelUploadForAdmin(ctx context.Context, uploadID, adminID, note string, artifactError *string) (*Upload, error) {
	now := nowString()
	var deletedAt any
	if artifactError == nil {
		deletedAt = now
	}
	result, err := s.db.ExecContext(ctx, `UPDATE uploads SET status = 'canceled', canceled_at = ?, canceled_by_user_id = ?, artifacts_deleted_at = ?, artifact_error = ?, admin_note = ?, updated_at = ? WHERE id = ?`,
		now, adminID, deletedAt, nullableStringPtr(artifactError), nullableNote(note), now, uploadID)
	if err != nil {
		return nil, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return nil, err
	}
	if affected != 1 {
		return nil, sql.ErrNoRows
	}
	return s.UploadByID(ctx, uploadID)
}

func (s *Store) QueuePosition(ctx context.Context, id string) (int, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id FROM jobs WHERE status = 'queued' ORDER BY created_at`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	pos := 1
	for rows.Next() {
		var jobID string
		if err := rows.Scan(&jobID); err != nil {
			return 0, err
		}
		if jobID == id {
			return pos, nil
		}
		pos++
	}
	return 0, rows.Err()
}

func (s *Store) CountJobsByStatus(ctx context.Context, statuses ...string) (int, error) {
	if len(statuses) == 0 {
		return 0, nil
	}
	placeholders := strings.TrimRight(strings.Repeat("?,", len(statuses)), ",")
	args := make([]any, 0, len(statuses))
	for _, status := range statuses {
		args = append(args, status)
	}
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE status IN (`+placeholders+`)`, args...).Scan(&count)
	return count, err
}

func (s *Store) CountUploadsByIPSince(ctx context.Context, ip string, since time.Time) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM uploads WHERE ip_address = ? AND created_at >= ?`, ip, formatTime(since)).Scan(&count)
	return count, err
}

func (s *Store) CountActiveUploadsByIP(ctx context.Context, ip string, activeSince time.Time) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM uploads WHERE ip_address = ? AND (status = 'assembling' OR (status = 'uploading' AND updated_at >= ?))`, ip, formatTime(activeSince)).Scan(&count)
	return count, err
}

func (s *Store) CountJobsByIPSince(ctx context.Context, ip string, since time.Time) (int, error) {
	var count int
	err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs j JOIN uploads u ON u.id = j.upload_id WHERE u.ip_address = ? AND j.created_at >= ?`, ip, formatTime(since)).Scan(&count)
	return count, err
}

func (s *Store) AddEvent(ctx context.Context, event Event) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO events(level, kind, actor_user_id, upload_id, job_id, message, metadata_json, ip_address, user_agent, created_at) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.Level, event.Kind, nullableStringPtr(event.ActorUserID), nullableStringPtr(event.UploadID), nullableStringPtr(event.JobID), event.Message, nullableStringPtr(event.MetadataJSON), nullableStringPtr(event.IPAddress), nullableStringPtr(event.UserAgent), formatTime(event.CreatedAt))
	return err
}

func (s *Store) ListEvents(ctx context.Context, limit int) ([]Event, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT id, level, kind, actor_user_id, upload_id, job_id, message, metadata_json, ip_address, user_agent, created_at FROM events ORDER BY created_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]Event, 0)
	for rows.Next() {
		var e Event
		var actor, uploadID, jobID, metadata, ip, ua sql.NullString
		var created string
		if err := rows.Scan(&e.ID, &e.Level, &e.Kind, &actor, &uploadID, &jobID, &e.Message, &metadata, &ip, &ua, &created); err != nil {
			return nil, err
		}
		e.ActorUserID = nullablePtr(actor)
		e.UploadID = nullablePtr(uploadID)
		e.JobID = nullablePtr(jobID)
		e.MetadataJSON = nullablePtr(metadata)
		e.IPAddress = nullablePtr(ip)
		e.UserAgent = nullablePtr(ua)
		e.CreatedAt = parseTime(created)
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) ListEventsFiltered(ctx context.Context, filter AdminEventFilter) ([]Event, int, error) {
	limit, offset := normalizeLimitOffset(filter.Limit, filter.Offset)
	where := []string{"1=1"}
	args := make([]any, 0)
	if filter.Level != "" {
		where = append(where, "level = ?")
		args = append(args, filter.Level)
	}
	if filter.Kind != "" {
		where = append(where, "kind = ?")
		args = append(args, filter.Kind)
	}
	if filter.JobID != "" {
		where = append(where, "job_id = ?")
		args = append(args, filter.JobID)
	}
	if filter.UploadID != "" {
		where = append(where, "upload_id = ?")
		args = append(args, filter.UploadID)
	}
	if filter.UserID != "" {
		where = append(where, "actor_user_id = ?")
		args = append(args, filter.UserID)
	}
	if strings.TrimSpace(filter.Query) != "" {
		q := "%" + strings.ToLower(strings.TrimSpace(filter.Query)) + "%"
		where = append(where, "(LOWER(message) LIKE ? OR LOWER(kind) LIKE ? OR LOWER(COALESCE(metadata_json, '')) LIKE ?)")
		args = append(args, q, q, q)
	}
	whereSQL := strings.Join(where, " AND ")
	var total int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM events WHERE `+whereSQL, args...).Scan(&total); err != nil {
		return nil, 0, err
	}
	queryArgs := append(append([]any{}, args...), limit, offset)
	rows, err := s.db.QueryContext(ctx, `SELECT id, level, kind, actor_user_id, upload_id, job_id, message, metadata_json, ip_address, user_agent, created_at FROM events WHERE `+whereSQL+` ORDER BY created_at DESC LIMIT ? OFFSET ?`, queryArgs...)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	out := make([]Event, 0)
	for rows.Next() {
		var e Event
		var actor, uploadID, jobID, metadata, ip, ua sql.NullString
		var created string
		if err := rows.Scan(&e.ID, &e.Level, &e.Kind, &actor, &uploadID, &jobID, &e.Message, &metadata, &ip, &ua, &created); err != nil {
			return nil, 0, err
		}
		e.ActorUserID = nullablePtr(actor)
		e.UploadID = nullablePtr(uploadID)
		e.JobID = nullablePtr(jobID)
		e.MetadataJSON = nullablePtr(metadata)
		e.IPAddress = nullablePtr(ip)
		e.UserAgent = nullablePtr(ua)
		e.CreatedAt = parseTime(created)
		out = append(out, e)
	}
	return out, total, rows.Err()
}

func (s *Store) Summary(ctx context.Context) (map[string]any, error) {
	out := make(map[string]any)
	for _, status := range []string{"queued", "converting", "finished", "error", "canceled"} {
		var count int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM jobs WHERE status = ?`, status).Scan(&count); err != nil {
			return nil, err
		}
		out[status+"Jobs"] = count
	}
	var activeUploads int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM uploads WHERE status IN ('uploading','assembling')`).Scan(&activeUploads); err != nil {
		return nil, err
	}
	out["activeUploads"] = activeUploads
	var bytesProcessed sql.NullInt64
	if err := s.db.QueryRowContext(ctx, `SELECT SUM(output_size_bytes) FROM jobs WHERE status = 'finished'`).Scan(&bytesProcessed); err != nil {
		return nil, err
	}
	if bytesProcessed.Valid {
		out["bytesProcessed"] = bytesProcessed.Int64
	} else {
		out["bytesProcessed"] = int64(0)
	}
	return out, nil
}

func (s *Store) CancelInactiveUploads(ctx context.Context, cutoff time.Time) ([]Upload, error) {
	now := nowString()
	rows, err := s.db.QueryContext(ctx, `UPDATE uploads SET status = 'canceled', canceled_at = ?, updated_at = ? WHERE status = 'uploading' AND updated_at < ? RETURNING `+uploadSelectColumns, now, now, formatTime(cutoff))
	if err != nil {
		return nil, err
	}
	inactive := make([]Upload, 0)
	for rows.Next() {
		upload, err := scanUploadRows(rows)
		if err != nil {
			rows.Close()
			return nil, err
		}
		inactive = append(inactive, *upload)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	rows.Close()
	return inactive, nil
}

func (s *Store) CleanupExpired(ctx context.Context, before time.Time) ([]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT source_path FROM uploads WHERE expires_at < ? AND source_path IS NOT NULL`, formatTime(before))
	if err != nil {
		return nil, err
	}
	var paths []string
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			rows.Close()
			return nil, err
		}
		paths = append(paths, path)
	}
	rows.Close()
	_, err = s.db.ExecContext(ctx, `DELETE FROM uploads WHERE expires_at < ? AND status IN ('uploading','complete','error','canceled')`, formatTime(before))
	return paths, err
}

func nowString() string {
	return formatTime(time.Now().UTC())
}

func formatTime(t time.Time) string {
	return t.UTC().Format(timeFormat)
}

func parseTime(v string) time.Time {
	t, _ := time.Parse(timeFormat, v)
	return t
}

func parseNullableTime(v sql.NullString) *time.Time {
	if !v.Valid || v.String == "" {
		return nil
	}
	t := parseTime(v.String)
	return &t
}

func nullableTime(t *time.Time) any {
	if t == nil {
		return nil
	}
	return formatTime(*t)
}

func nullableStringPtr(s *string) any {
	if s == nil {
		return nil
	}
	return *s
}

func nullableNote(note string) any {
	note = strings.TrimSpace(note)
	if note == "" {
		return nil
	}
	return note
}

func nullableInt64Ptr(v *int64) any {
	if v == nil {
		return nil
	}
	return *v
}

func nullablePtr(v sql.NullString) *string {
	if !v.Valid {
		return nil
	}
	return &v.String
}

func boolInt(v bool) int {
	if v {
		return 1
	}
	return 0
}

func normalizeLimitOffset(limit, offset int) (int, int) {
	if limit <= 0 || limit > 1000 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}

func prefixColumns(prefix, columns string) string {
	parts := strings.Split(columns, ",")
	for i, part := range parts {
		parts[i] = prefix + "." + strings.TrimSpace(part)
	}
	return strings.Join(parts, ", ")
}

type userScanner interface {
	Scan(dest ...any) error
}

func scanUser(row userScanner) (*User, error) {
	var u User
	var created, updated string
	var lastLogin sql.NullString
	if err := row.Scan(&u.ID, &u.Email, &u.PasswordHash, &u.Role, &u.Disabled, &created, &updated, &lastLogin); err != nil {
		return nil, err
	}
	u.CreatedAt = parseTime(created)
	u.UpdatedAt = parseTime(updated)
	u.LastLoginAt = parseNullableTime(lastLogin)
	return &u, nil
}

func scanUserRows(rows *sql.Rows) (*User, error) {
	return scanUser(rows)
}

type uploadScanner interface {
	Scan(dest ...any) error
}

func scanUpload(row uploadScanner) (*Upload, error) {
	var u Upload
	var owner, anon, source, mediaType, mime, canceledBy, artifactError, adminNote sql.NullString
	var created, updated, expires string
	var canceledAt, artifactsDeletedAt sql.NullString
	if err := row.Scan(&u.ID, &owner, &anon, &u.OriginalFilename, &source, &mediaType, &mime, &u.SizeBytes, &u.BytesReceived, &u.ChunkSizeBytes, &u.ChunkCount, &u.Status, &u.IPAddress, &u.UserAgent, &created, &updated, &expires, &canceledAt, &canceledBy, &artifactsDeletedAt, &artifactError, &adminNote); err != nil {
		return nil, err
	}
	u.OwnerUserID = nullablePtr(owner)
	u.AnonymousTokenHash = nullablePtr(anon)
	u.SourcePath = nullablePtr(source)
	u.MediaType = nullablePtr(mediaType)
	u.DetectedMIME = nullablePtr(mime)
	u.CreatedAt = parseTime(created)
	u.UpdatedAt = parseTime(updated)
	u.ExpiresAt = parseTime(expires)
	u.CanceledAt = parseNullableTime(canceledAt)
	u.CanceledByUserID = nullablePtr(canceledBy)
	u.ArtifactsDeletedAt = parseNullableTime(artifactsDeletedAt)
	u.ArtifactError = nullablePtr(artifactError)
	u.AdminNote = nullablePtr(adminNote)
	return &u, nil
}

func scanUploadRows(rows *sql.Rows) (*Upload, error) {
	return scanUpload(rows)
}

type jobScanner interface {
	Scan(dest ...any) error
}

func scanJob(row jobScanner) (*Job, error) {
	var j Job
	var owner, anon, output, errMsg, removedBy, artifactError, adminNote sql.NullString
	var size sql.NullInt64
	var started, finished, created, updated, removedAt, artifactsDeletedAt sql.NullString
	if err := row.Scan(&j.ID, &j.UploadID, &owner, &anon, &j.Status, &j.TargetFormat, &j.Preset, &j.OptionsJSON, &j.ProgressPercentage, &output, &size, &errMsg, &started, &finished, &created, &updated, &removedAt, &removedBy, &artifactsDeletedAt, &artifactError, &adminNote); err != nil {
		return nil, err
	}
	j.OwnerUserID = nullablePtr(owner)
	j.AnonymousTokenHash = nullablePtr(anon)
	j.OutputPath = nullablePtr(output)
	if size.Valid {
		j.OutputSizeBytes = &size.Int64
	}
	j.ErrorMessage = nullablePtr(errMsg)
	j.StartedAt = parseNullableTime(started)
	j.FinishedAt = parseNullableTime(finished)
	if created.Valid {
		j.CreatedAt = parseTime(created.String)
	}
	if updated.Valid {
		j.UpdatedAt = parseTime(updated.String)
	}
	j.RemovedAt = parseNullableTime(removedAt)
	j.RemovedByUserID = nullablePtr(removedBy)
	j.ArtifactsDeletedAt = parseNullableTime(artifactsDeletedAt)
	j.ArtifactError = nullablePtr(artifactError)
	j.AdminNote = nullablePtr(adminNote)
	return &j, nil
}

func scanJobRows(rows *sql.Rows) (*Job, error) {
	return scanJob(rows)
}
