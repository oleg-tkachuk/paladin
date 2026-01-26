package httpapi

import "time"

type ErrorResponse struct {
	Error   string `json:"error"`
	Details string `json:"details,omitempty"`
	TraceID string `json:"trace_id,omitempty"`
}

type VersionResponse struct {
	Service   string `json:"service"`
	Version   string `json:"version"`
	Commit    string `json:"commit"`
	BuildTime string `json:"build_time"`
}

type CreateObjectRequest struct {
	ContentType string            `binding:"required" json:"content_type"`
	SizeBytes   int64             `binding:"required,min=1" json:"size_bytes"`
	Labels      map[string]string `json:"labels,omitempty"`
	ExternalRef *string           `json:"external_ref,omitempty"`
}

type CreateObjectResponse struct {
	ObjectID  string            `json:"object_id"`
	ObjectKey string            `json:"object_key"`
	UploadURL string            `json:"upload_url"`
	Method    string            `json:"method"`
	Headers   map[string]string `json:"headers,omitempty"`
	ExpiresAt time.Time         `json:"expires_at"`
}

type GetObjectResponse struct {
	ObjectID    string            `json:"object_id"`
	ObjectKey   string            `json:"object_key"`
	Bucket      string            `json:"bucket"`
	ContentType string            `json:"content_type"`
	SizeBytes   int64             `json:"size_bytes"`
	Status      string            `json:"status"`
	Labels      map[string]string `json:"labels,omitempty"`
	ExternalRef *string           `json:"external_ref,omitempty"`
	DownloadURL string            `json:"download_url"`
	ExpiresAt   time.Time         `json:"expires_at"`
}

type GetObjectMetaResponse struct {
	ObjectID    string            `json:"object_id"`
	ObjectKey   string            `json:"object_key"`
	Bucket      string            `json:"bucket"`
	ContentType string            `json:"content_type"`
	SizeBytes   int64             `json:"size_bytes"`
	Status      string            `json:"status"`
	Labels      map[string]string `json:"labels,omitempty"`
	ExternalRef *string           `json:"external_ref,omitempty"`
	ExpiresAt   *time.Time        `json:"expires_at,omitempty"`
}

type CompleteObjectResponse struct {
	Status string `json:"status"`
}

type DeleteObjectResponse struct {
	Status string `json:"status"`
}

type InitiateMultipartRequest struct {
	ContentType string            `binding:"required" json:"content_type"`
	SizeBytes   int64             `binding:"required,min=1" json:"size_bytes"`
	Labels      map[string]string `json:"labels,omitempty"`
	ExternalRef *string           `json:"external_ref,omitempty"`
}

type InitiateMultipartResponse struct {
	ObjectID  string    `json:"object_id"`
	ObjectKey string    `json:"object_key"`
	UploadID  string    `json:"upload_id"`
	PartSize  int64     `json:"part_size"`
	ExpiresAt time.Time `json:"expires_at"`
}

type SignPartResponse struct {
	UploadURL string    `json:"upload_url"`
	Method    string    `json:"method"`
	ExpiresAt time.Time `json:"expires_at"`
}

type CompleteMultipartRequest struct {
	Parts []CompletePart `binding:"required" json:"parts"`
}

type CompletePart struct {
	PartNumber int32  `binding:"required" json:"part_number"`
	ETag       string `binding:"required" json:"etag"`
}

type CompleteMultipartResponse struct {
	ObjectID string `json:"object_id"`
	Status   string `json:"status"`
}

type AbortMultipartResponse struct {
	Status string `json:"status"`
}
