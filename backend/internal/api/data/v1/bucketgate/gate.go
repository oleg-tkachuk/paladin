// Package bucketgate decides whether the state of a collection's bucket and
// of the storage backend holding it lets an operation through. It is the one
// place that decision is made: the bucket resolver applies it to every
// collection→bucket lookup, so every data-plane RPC that resolves a bucket
// gets the same answer for the same state.
//
// The gate asks what an operation touches, not only whether it writes. Each
// operation names its Access: BytesWrite puts or removes bytes on the backend,
// Read reads them, MetadataWrite changes only control-plane rows, and
// Protective is a control that keeps content from being served. A draining
// backend still holds and serves its bytes, so only a write of bytes there is
// refused; a row change is not, and a protective control is never refused on
// state, since refusing it would leave content readable that someone has
// flagged as unsafe.
//
// The table, by state: the accesses it refuses, and the code and ErrorInfo
// reason the refusal answers.
//
//	state                          refuses                           code                 reason
//	backend enabled, bucket ready  nothing
//	backend disabled               bytes-write, read, metadata-write FAILED_PRECONDITION  ERROR_REASON_BACKEND_DISABLED
//	backend read-only (draining)   bytes-write                       FAILED_PRECONDITION  ERROR_REASON_BACKEND_READ_ONLY
//	bucket pending (provisioning)  bytes-write                       UNAVAILABLE          ERROR_REASON_BUCKET_PROVISIONING
//	bucket failed / deleting       bytes-write                       FAILED_PRECONDITION  ERROR_REASON_FAILED_PRECONDITION
//	backend in maintenance         nothing (an advisory label, not a gate)
//
// Protective is refused in no state.
//
// FAILED_PRECONDITION is a state only an operator changes, so a retry does not
// help; UNAVAILABLE is one the reconciler leaves on its own, so a retry does.
// Where two states hold at once the more final one answers: disabled, then
// draining, then the bucket's own state. A provision state the gate does not
// know refuses a bytes-write as not ready rather than letting it through.
//
// The package is a leaf on purpose: the store adapter, the handlers and their
// test fakes all import it, so it carries no dependency beyond the codes.
package bucketgate

import (
	"errors"

	"connectrpc.com/connect/v2"

	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
)

// Access is what an operation touches. Only the values below exist: the kind
// is unexported, so no other package can name an access the gate does not
// know. The zero value is BytesWrite, the strictest, so an access left unset
// is refused wherever a write of bytes would be.
type Access struct{ kind accessKind }

type accessKind uint8

const (
	kindBytesWrite accessKind = iota
	kindRead
	kindMetadataWrite
	kindProtective
)

// The accesses an operation can name.
var (
	// BytesWrite puts or removes bytes on the backend: an upload, a copy's
	// destination, a presigned PUT, a multipart upload, a promote, a
	// permanent delete, a version restore.
	BytesWrite = Access{kindBytesWrite}
	// Read reads bytes, or resolves a bucket only to name or scope a read:
	// GET, HEAD, list, presign GET, a copy's source.
	Read = Access{kindRead}
	// MetadataWrite changes control-plane rows only and leaves the backend's
	// bytes as they are: metadata and tags, a move to the trash and back out
	// of it, Object Lock retention and legal hold.
	MetadataWrite = Access{kindMetadataWrite}
	// Protective is a control that keeps content from being served — a taint
	// flag, set or cleared. Cedar decides it; the bucket's state does not.
	Protective = Access{kindProtective}
)

// DeleteAccess is what a delete touches: a permanent one removes bytes from
// the backend, a move to the trash only changes the object's row.
func DeleteAccess(permanent bool) Access {
	if permanent {
		return BytesWrite
	}
	return MetadataWrite
}

// The names String gives the accesses, as logs and test output print them.
const (
	nameBytesWrite    = "bytes-write"
	nameRead          = "read"
	nameMetadataWrite = "metadata-write"
	nameProtective    = "protective"
)

// String names the access.
func (a Access) String() string {
	switch a {
	case Read:
		return nameRead
	case MetadataWrite:
		return nameMetadataWrite
	case Protective:
		return nameProtective
	default:
		return nameBytesWrite
	}
}

// The provision states a bucket row carries (buckets.provision_state).
const (
	// ProvisionPending: registered, and the reconciler still owes the backend
	// the bucket.
	ProvisionPending = "pending"
	// ProvisionReady: the backend holds the bucket.
	ProvisionReady = "ready"
	// ProvisionFailed: creating it on the backend failed for good; an operator
	// retries it.
	ProvisionFailed = "failed"
	// ProvisionDeleting: the bucket is being removed from the backend.
	ProvisionDeleting = "deleting"
)

// State is what the gate decides on: the backend's operator flags and the
// bucket's provision state. The backend's maintenance flag is advisory and is
// deliberately not part of it.
type State struct {
	Enabled        bool
	ReadOnly       bool
	ProvisionState string
}

// Refusal is an operation the gate refuses, with the code and reason it
// answers on the wire. Compare with errors.Is against the variables below.
type Refusal struct {
	msg    string
	Code   connect.Code
	Reason commonv1.ErrorReason
}

func (r *Refusal) Error() string { return r.msg }

// The refusals Check answers.
var (
	// ErrBackendDisabled: the backend serves no Paladin-mediated operation.
	// This cannot revoke presigned URLs already issued; those reach the
	// backend directly and expire on their own.
	ErrBackendDisabled = &Refusal{
		msg: "storage backend is disabled", Code: connect.CodeFailedPrecondition,
		Reason: commonv1.ErrorReason_ERROR_REASON_BACKEND_DISABLED,
	}
	// ErrBackendReadOnly: the backend is draining — data is being moved off
	// it before it is disabled — so it serves reads and refuses writes of
	// bytes.
	ErrBackendReadOnly = &Refusal{
		msg: "storage backend is read-only (draining)", Code: connect.CodeFailedPrecondition,
		Reason: commonv1.ErrorReason_ERROR_REASON_BACKEND_READ_ONLY,
	}
	// ErrBucketProvisioning: the bucket is registered and the backend does
	// not hold it yet (ADR-0015). Temporary: the reconciler creates it.
	ErrBucketProvisioning = &Refusal{
		msg: "storage bucket is still provisioning", Code: connect.CodeUnavailable,
		Reason: commonv1.ErrorReason_ERROR_REASON_BUCKET_PROVISIONING,
	}
	// ErrBucketNotReady: the bucket failed to provision or is being deleted,
	// so the backend may not hold it, and waiting will not change that.
	ErrBucketNotReady = &Refusal{
		msg: "storage bucket is not ready for writes", Code: connect.CodeFailedPrecondition,
		Reason: commonv1.ErrorReason_ERROR_REASON_FAILED_PRECONDITION,
	}
)

// Refusals lists every refusal Check answers, for registering them with the
// error mapper.
func Refusals() []*Refusal {
	return []*Refusal{ErrBackendDisabled, ErrBackendReadOnly, ErrBucketProvisioning, ErrBucketNotReady}
}

// Refused reports whether err, however wrapped, is one of the gate's
// refusals: a verdict on the bucket's state, logged as a refusal even when
// its code is Unavailable, as a provisioning bucket's is. A gate that could
// not decide — its state unreadable — answers no Refusal, and stays the
// server's fault.
func Refused(err error) bool {
	var r *Refusal
	return errors.As(err, &r)
}

// Check answers whether s lets an operation of access through: nil, or the
// refusal the package table names.
func Check(access Access, s State) error {
	if access == Protective {
		return nil
	}
	if !s.Enabled {
		return ErrBackendDisabled
	}
	if access == Read || access == MetadataWrite {
		return nil
	}
	if s.ReadOnly {
		return ErrBackendReadOnly
	}
	switch s.ProvisionState {
	case ProvisionReady:
		return nil
	case ProvisionPending:
		return ErrBucketProvisioning
	default:
		return ErrBucketNotReady
	}
}
