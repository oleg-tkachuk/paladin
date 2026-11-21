// Package bucketgate decides whether the state of a collection's bucket and
// of the storage backend holding it lets an operation through. It is the one
// place that decision is made: the bucket resolver applies it to every
// collection→bucket lookup, so every data-plane RPC that resolves a bucket for
// a write gets the same answer for the same state.
//
// The table, by state: what it refuses, and the code and ErrorInfo reason the
// refusal answers.
//
//	state                          refuses         code                 reason
//	backend enabled, bucket ready  nothing
//	backend disabled               reads, writes   FAILED_PRECONDITION  ERROR_REASON_BACKEND_DISABLED
//	backend read-only (draining)   writes          FAILED_PRECONDITION  ERROR_REASON_BACKEND_READ_ONLY
//	bucket pending (provisioning)  writes          UNAVAILABLE          ERROR_REASON_BUCKET_PROVISIONING
//	bucket failed / deleting       writes          FAILED_PRECONDITION  ERROR_REASON_FAILED_PRECONDITION
//	backend in maintenance         nothing (an advisory label, not a gate)
//
// FAILED_PRECONDITION is a state only an operator changes, so a retry does not
// help; UNAVAILABLE is one the reconciler leaves on its own, so a retry does.
// Where two states hold at once the more final one answers: disabled, then
// draining, then the bucket's own state. A provision state the gate does not
// know refuses a write as not ready rather than letting it through.
//
// The package is a leaf on purpose: the store adapter, the handlers and their
// test fakes all import it, so it carries no dependency beyond the codes.
package bucketgate

import (
	"connectrpc.com/connect/v2"

	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
)

// Access is what an operation does to the bucket.
type Access uint8

const (
	// Read leaves the bucket as it is: GET, HEAD, list, presign GET.
	Read Access = iota + 1
	// Write changes it or the objects in it. Any access that is not Read is
	// gated as a write.
	Write
)

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
	// it before it is disabled — so it serves reads and refuses writes.
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

// Check answers whether s lets an operation of access through: nil, or the
// refusal the package table names.
func Check(access Access, s State) error {
	if !s.Enabled {
		return ErrBackendDisabled
	}
	if access == Read {
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

// AccessOf is the access a resolver's write flag stands for.
func AccessOf(write bool) Access {
	if write {
		return Write
	}
	return Read
}
