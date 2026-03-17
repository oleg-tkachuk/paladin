package service

const (
	// Operation names
	OpCreateObject      = "CreateObject"
	OpGetObject         = "GetObject"
	OpDeleteObject      = "DeleteObject"
	OpRestoreObject     = "RestoreObject"
	OpPurgeObject       = "PurgeObject"
	OpListObjects       = "ListObjects"
	OpUpdateStatus      = "UpdateStatus"
	OpCompleteObject    = "CompleteObject"
	OpSignUpload        = "SignUpload"
	OpSignDownload      = "SignDownload"
	OpInitiateMultipart = "InitiateMultipart"
	OpCompleteMultipart = "CompleteMultipart"
	OpAbortMultipart    = "AbortMultipart"
	OpSignPart          = "SignPart"
	OpSignPartsBatch    = "SignPartsBatch"
	OpGetMultipart      = "GetMultipart"
	OpGetStats          = "GetStats"

	// Log messages
	LogObjectCreated       = "Object Created"
	LogObjectSoftDeleted   = "Object Soft Deleted"
	LogObjectRestored      = "Object Restored"
	LogObjectPurged        = "Object Purged (Hard Deleted)"
	LogObjectStatusUpdated = "Object Status Updated"
	LogMultipartInitiated  = "Multipart Upload Initiated"
	LogMultipartCompleted  = "Multipart Upload Completed"
	LogMultipartAborted    = "Multipart Upload Aborted"

	// Error messages
	ErrInvalidTransition = "invalid transition"
	ErrNotFound          = "not found"
	ErrUnauthorized      = "unauthorized"
	ErrConflict          = "conflict"

	// Constants for observability and resilience
	TracerName      = "object-service"
	S3DeleteBreaker = "s3_delete"
)
