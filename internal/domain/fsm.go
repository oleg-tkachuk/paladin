package domain

import (
	"github.com/qmuntal/stateless"
)

// ObjectEvent represents a business event that transitions an object's state.
type ObjectEvent string

const (
	EventObjectUploadComplete ObjectEvent = "upload_complete"
	EventObjectSoftDelete     ObjectEvent = "soft_delete"
	EventObjectRestore        ObjectEvent = "restore"
	EventObjectHardDelete     ObjectEvent = "hard_delete"
)

// NewObjectFSM creates a stateless machine for Object state transitions.
// It uses ObjectStatus as the state type and ObjectEvent as the trigger.
func NewObjectFSM(initialState ObjectStatus) *stateless.StateMachine {
	sm := stateless.NewStateMachine(initialState)

	// Pending
	sm.Configure(ObjectPending).
		Permit(EventObjectSoftDelete, ObjectSoftDeleted)

	// Uploading (Reserved for iterative uploads, unused in v1 CreateSingle, but defined just in case)
	sm.Configure(ObjectUploading).
		Permit(EventObjectUploadComplete, ObjectComplete).
		Permit(EventObjectSoftDelete, ObjectSoftDeleted)

	// Uploaded (If tracking intermediate state before verification, same as pending/uploading for now)
	sm.Configure(ObjectUploaded).
		Permit(EventObjectUploadComplete, ObjectComplete).
		Permit(EventObjectSoftDelete, ObjectSoftDeleted)

	// Complete
	sm.Configure(ObjectComplete).
		Ignore(EventObjectUploadComplete). // Idempotent completion
		Permit(EventObjectSoftDelete, ObjectSoftDeleted)

	// SoftDeleted
	sm.Configure(ObjectSoftDeleted).
		Ignore(EventObjectSoftDelete).              // Idempotent soft-delete
		Permit(EventObjectRestore, ObjectComplete). // Assuming it goes back to complete, adjust if pending is possible
		Permit(EventObjectHardDelete, ObjectHardDeleted)

	// Deleted (Legacy or other workflows, map soft/hard rules)
	sm.Configure(ObjectDeleted).
		Ignore(EventObjectSoftDelete).
		Permit(EventObjectRestore, ObjectComplete).
		Permit(EventObjectHardDelete, ObjectHardDeleted)

	// HardDeleted
	sm.Configure(ObjectHardDeleted).
		Ignore(EventObjectHardDelete) // Idempotent hard-delete
		// If we don't map it, Fire() will return an error, which is correct (conflict).

	// Let's remove the Ignored soft-delete on HardDeleted so it explicitly fails.
	return sm
}

// MultipartEvent represents an event that transitions a multipart upload's state.
type MultipartEvent string

const (
	EventMultipartComplete MultipartEvent = "complete"
	EventMultipartAbort    MultipartEvent = "abort"
	// Expired might be triggered by a background worker
	EventMultipartExpire MultipartEvent = "expire"
)

// NewMultipartFSM creates a stateless machine for Multipart state transitions.
func NewMultipartFSM(initialState MultipartStatus) *stateless.StateMachine {
	sm := stateless.NewStateMachine(initialState)

	sm.Configure(MultipartInitiated).
		Permit(EventMultipartComplete, MultipartCompleted).
		Permit(EventMultipartAbort, MultipartAborted).
		Permit(EventMultipartExpire, MultipartExpired)

	sm.Configure(MultipartCompleted).
		Ignore(EventMultipartComplete) // Idempotent completion

	sm.Configure(MultipartAborted).
		Ignore(EventMultipartAbort) // Idempotent abort

	sm.Configure(MultipartExpired).
		Ignore(EventMultipartExpire)

	return sm
}
