package store

import "time"

type User struct {
	ID           string     `json:"id"`
	Email        string     `json:"email"`
	PasswordHash string     `json:"-"`
	Role         string     `json:"role"`
	Disabled     bool       `json:"disabled"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
	LastLoginAt  *time.Time `json:"lastLoginAt,omitempty"`
}

type Session struct {
	ID        string
	TokenHash string
	UserID    string
	CSRFToken string
	ExpiresAt time.Time
	CreatedAt time.Time
	IPAddress string
	UserAgent string
}

type Upload struct {
	ID                 string    `json:"id"`
	OwnerUserID        *string   `json:"ownerUserId,omitempty"`
	AnonymousTokenHash *string   `json:"-"`
	OriginalFilename   string    `json:"originalFilename"`
	SourcePath         *string   `json:"sourcePath,omitempty"`
	MediaType          *string   `json:"mediaType,omitempty"`
	DetectedMIME       *string   `json:"detectedMime,omitempty"`
	SizeBytes          int64     `json:"sizeBytes"`
	BytesReceived      int64     `json:"bytesReceived"`
	ChunkSizeBytes     int64     `json:"chunkSizeBytes"`
	ChunkCount         int       `json:"chunkCount"`
	Status             string    `json:"status"`
	IPAddress          string    `json:"ipAddress"`
	UserAgent          string    `json:"userAgent"`
	CreatedAt          time.Time `json:"createdAt"`
	UpdatedAt          time.Time `json:"updatedAt"`
	ExpiresAt          time.Time `json:"expiresAt"`
}

type UploadChunk struct {
	UploadID   string
	Index      int
	SizeBytes  int64
	SHA256     *string
	Path       string
	ReceivedAt time.Time
}

type Job struct {
	ID                 string     `json:"id"`
	UploadID           string     `json:"uploadId"`
	OwnerUserID        *string    `json:"ownerUserId,omitempty"`
	AnonymousTokenHash *string    `json:"-"`
	Status             string     `json:"status"`
	TargetFormat       string     `json:"targetFormat"`
	Preset             string     `json:"preset"`
	OptionsJSON        string     `json:"optionsJson"`
	ProgressPercentage int        `json:"progressPercentage"`
	OutputPath         *string    `json:"outputPath,omitempty"`
	OutputSizeBytes    *int64     `json:"outputSizeBytes,omitempty"`
	ErrorMessage       *string    `json:"error,omitempty"`
	StartedAt          *time.Time `json:"startedAt,omitempty"`
	FinishedAt         *time.Time `json:"finishedAt,omitempty"`
	CreatedAt          time.Time  `json:"createdAt"`
	UpdatedAt          time.Time  `json:"updatedAt"`
}

type Event struct {
	ID           int64     `json:"id"`
	Level        string    `json:"level"`
	Kind         string    `json:"kind"`
	ActorUserID  *string   `json:"actorUserId,omitempty"`
	UploadID     *string   `json:"uploadId,omitempty"`
	JobID        *string   `json:"jobId,omitempty"`
	Message      string    `json:"message"`
	MetadataJSON *string   `json:"metadataJson,omitempty"`
	IPAddress    *string   `json:"ipAddress,omitempty"`
	UserAgent    *string   `json:"userAgent,omitempty"`
	CreatedAt    time.Time `json:"createdAt"`
}
