package objecth

import (
	"context"
	"errors"
	"fmt"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/metrics"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/capability"
)

// Object Lock (ADR-0013): write-once-read-many retention on an object
// version, in the two modes S3 defines.
//
// GOVERNANCE is a control with an override — a caller holding
// `lock.governance.bypass` (or `platform.admin`) can shorten a window or
// delete through it. COMPLIANCE has no override at all: not the tenant admin,
// not the platform admin, not the person who set it. That asymmetry is the
// entire point. A control that its own operator can lift on request is not
// evidence of anything, which is what regulators asking for WORM actually
// want. It also means COMPLIANCE is dangerous in a way GOVERNANCE is not: a
// mistaken hundred-year window is unrecoverable, and the storage it pins is
// paid for until it expires. Hence a Cedar action of its own rather than
// riding along with UpdateObject.
//
// Legal hold is orthogonal: no expiry, blocks deletion while on, and — unlike
// retention — can be lifted by anyone authorised to set it. A hold answers
// "preserve this until the matter closes", where the closing date is not
// knowable in advance.
//
// Locks attach to a version, never to an object. On an unversioned bucket
// there is exactly one version and the distinction never surfaces; on a
// versioned one it is the difference between protecting a document and
// protecting today's copy of it.

// ErrRetentionWeakened is returned when a write would shorten, downgrade or
// clear an active retention window that the caller may not weaken.
var ErrRetentionWeakened = errors.New("retention cannot be shortened, downgraded or cleared")

// ErrObjectLockNotEnabled is returned when the parent bucket has no object
// lock configured. Locking through a bucket that never opted in would leave
// undeletable objects in a bucket whose operator never agreed to hold them.
var ErrObjectLockNotEnabled = errors.New("object lock is not enabled on the parent bucket")

// ErrNoCurrentVersion is returned when an object has no current version to
// attach a lock to.
var ErrNoCurrentVersion = errors.New("object has no current version to lock")

// LockRepository is the persistence port for object locks. It is separate
// from VersionRepository because a deployment can run versioning without
// object lock, and the reverse is not true — locks need a version to hang on.
type LockRepository interface {
	// SetRetention applies or extends a retention window. Returns
	// ErrRetentionWeakened when the write would weaken an existing window the
	// caller may not weaken; the decision is made in SQL so two concurrent
	// callers cannot each read a long window and each write a shorter one.
	SetRetention(ctx context.Context, args SetRetentionArgs) (ObjectLock, error)

	// SetLegalHold turns a hold on or off. Releasing the last assertion on a
	// row removes the row, which runs through the retention trigger — so a
	// version still inside a COMPLIANCE window keeps its row and its lock.
	SetLegalHold(ctx context.Context, tenantID, versionID uuid.UUID, hold bool) (ObjectLock, error)

	// GetByVersion reads the lock state. A version with no lock returns a
	// zero ObjectLock and no error: "unlocked" is a state, not a miss.
	GetByVersion(ctx context.Context, versionID uuid.UUID) (ObjectLock, error)

	// ApplyBucketDefault puts a freshly promoted version under the parent
	// bucket's default retention. Never overwrites an existing lock — an
	// explicit retention that arrived first outranks a default.
	ApplyBucketDefault(ctx context.Context, tenantID, versionID uuid.UUID, mode string, retention time.Duration) error
}

// SetRetentionArgs is the input to LockRepository.SetRetention.
type SetRetentionArgs struct {
	TenantID    uuid.UUID
	VersionID   uuid.UUID
	Mode        string
	RetainUntil time.Time
	// BypassGovernance permits weakening an active GOVERNANCE window. It has
	// no effect on COMPLIANCE — the SQL ignores it there rather than trusting
	// every caller to remember.
	BypassGovernance bool
}

// LockHandler implements the data-plane object-lock RPCs. A sibling of
// *Handler and *VersionHandler for the same reason they are siblings: object
// lock is opt-in, and a deployment that does not wire it should not be forced
// to supply a repository for it.
type LockHandler struct {
	objects  Repository
	versions VersionRepository
	locks    LockRepository
	policy   cedar.Authorizer
}

func NewLockHandler(objects Repository, versions VersionRepository, locks LockRepository, policy cedar.Authorizer) *LockHandler {
	return &LockHandler{objects: objects, versions: versions, locks: locks, policy: policy}
}

// SetRetentionInput is the parsed form of SetObjectRetentionRequest.
type SetRetentionInput struct {
	Collection       string
	ObjectID         string
	Mode             string
	RetainUntil      time.Time
	BypassGovernance bool
}

// SetRetention applies or extends a retention window on the object's current
// version.
func (h *LockHandler) SetRetention(ctx context.Context, in SetRetentionInput) (ObjectLock, error) {
	if in.Mode != "GOVERNANCE" && in.Mode != "COMPLIANCE" {
		return ObjectLock{}, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("mode must be GOVERNANCE or COMPLIANCE, got %q", in.Mode))
	}
	if !in.RetainUntil.After(time.Now()) {
		// A window that has already closed asserts nothing but leaves a row
		// implying it does. Refusing is clearer than storing a no-op.
		return ObjectLock{}, connect.NewError(connect.CodeInvalidArgument,
			errors.New("retain_until must be in the future"))
	}

	res, err := h.resolve(ctx, in.Collection, in.ObjectID, cedar.ActionSetObjectRetention)
	if err != nil {
		return ObjectLock{}, err
	}
	if !res.meta.ObjectLockEnabled {
		return ObjectLock{}, connect.NewError(connect.CodeFailedPrecondition, ErrObjectLockNotEnabled)
	}

	// The bypass flag is a request to weaken a control, so it is gated on a
	// role rather than on the Cedar action that got us here — the same gate
	// DeleteObject uses, and for the same reason.
	if in.BypassGovernance && !res.principal.HasRole("lock.governance.bypass") &&
		!res.principal.HasRole(apiutil.RolePlatformAdmin) {
		return ObjectLock{}, connect.NewError(connect.CodePermissionDenied,
			errors.New("bypass_governance_retention requires role lock.governance.bypass or platform.admin"))
	}

	lock, err := h.locks.SetRetention(ctx, SetRetentionArgs{
		TenantID:         res.tenantID,
		VersionID:        res.versionID,
		Mode:             in.Mode,
		RetainUntil:      in.RetainUntil,
		BypassGovernance: in.BypassGovernance,
	})
	if err != nil {
		if errors.Is(err, ErrRetentionWeakened) {
			metrics.RecordObjectLock(ctx, "retention", in.Mode, "refused_weakening")
			return ObjectLock{}, connect.NewError(connect.CodeFailedPrecondition, err)
		}
		metrics.RecordObjectLock(ctx, "retention", in.Mode, "error")
		return ObjectLock{}, connect.NewError(connect.CodeInternal, err)
	}
	// A COMPLIANCE window cannot be shortened by anyone, so every one of these
	// is an irreversible commitment of storage until its date passes. Counting
	// them is the only way an operator learns how much of that has accumulated
	// before the bill or the capacity alert does the telling.
	outcome := "applied"
	if in.BypassGovernance {
		outcome = "applied_with_bypass"
	}
	metrics.RecordObjectLock(ctx, "retention", in.Mode, outcome)
	return lock, nil
}

// SetLegalHold turns a legal hold on or off on the object's current version.
func (h *LockHandler) SetLegalHold(ctx context.Context, collection, objectID string, hold bool) (ObjectLock, error) {
	res, err := h.resolve(ctx, collection, objectID, cedar.ActionSetObjectLegalHold)
	if err != nil {
		return ObjectLock{}, err
	}
	// Placing a hold requires the bucket to have opted in, for the same
	// reason retention does. Releasing one does not: a bucket whose lock
	// setting was turned off afterwards must still be releasable, or the hold
	// outlives every way of lifting it.
	if hold && !res.meta.ObjectLockEnabled {
		return ObjectLock{}, connect.NewError(connect.CodeFailedPrecondition, ErrObjectLockNotEnabled)
	}

	lock, err := h.locks.SetLegalHold(ctx, res.tenantID, res.versionID, hold)
	if err != nil {
		metrics.RecordObjectLock(ctx, "legal_hold", "", "error")
		return ObjectLock{}, connect.NewError(connect.CodeInternal, err)
	}
	outcome := "released"
	if hold {
		outcome = "placed"
	}
	metrics.RecordObjectLock(ctx, "legal_hold", "", outcome)
	return lock, nil
}

// GetLock reads the lock on the object's current version.
func (h *LockHandler) GetLock(ctx context.Context, collection, objectID string) (ObjectLock, error) {
	res, err := h.resolve(ctx, collection, objectID, cedar.ActionReadObjectLock)
	if err != nil {
		return ObjectLock{}, err
	}
	lock, err := h.locks.GetByVersion(ctx, res.versionID)
	if err != nil {
		return ObjectLock{}, connect.NewError(connect.CodeInternal, err)
	}
	return lock, nil
}

// resolved carries what every lock RPC needs after authentication,
// authorisation and resolution of the object to a concrete version.
type resolved struct {
	tenantID  uuid.UUID
	principal *auth.Principal
	obj       Object
	meta      BucketMeta
	versionID uuid.UUID
}

// resolve runs the shared preamble: caller identity, the object, its bucket,
// the Cedar decision, and the version the lock attaches to.
func (h *LockHandler) resolve(ctx context.Context, collection, objectID, action string) (resolved, error) {
	var out resolved
	tenantID, principal, err := apiutil.ActingContext(ctx)
	if err != nil {
		return out, err
	}
	if collection == "" || objectID == "" {
		return out, connect.NewError(connect.CodeInvalidArgument,
			errors.New("collection and object_id are required"))
	}
	obj, err := h.objects.FindByName(ctx, tenantID, collection, objectID)
	if err != nil {
		return out, connect.NewError(connect.CodeNotFound, err)
	}
	meta, err := h.objects.LookupBucketMeta(ctx, tenantID, collection, true) // lock write is a mutation
	if err != nil {
		return out, MapResolveErr(err)
	}
	if err := auth.AssertCapabilityOp(ctx, capability.OpManage, ""); err != nil {
		return out, err
	}
	if err := h.authorizeLock(ctx, principal, tenantID, obj, meta, action); err != nil {
		return out, err
	}

	versionID, err := h.versions.CurrentVersionID(ctx, obj.ObjectID)
	if err != nil {
		return out, connect.NewError(connect.CodeInternal, err)
	}
	if versionID == uuid.Nil {
		// Every promoted object gets a version row, so this is either an
		// object still PENDING or one written before versioning existed.
		// Either way there is nothing to attach retention to, and silently
		// succeeding would report a lock that protects nothing.
		return out, connect.NewError(connect.CodeFailedPrecondition, ErrNoCurrentVersion)
	}

	out = resolved{
		tenantID:  tenantID,
		principal: principal,
		obj:       obj,
		meta:      meta,
		versionID: versionID,
	}
	return out, nil
}

func (h *LockHandler) authorizeLock(
	ctx context.Context,
	principal *auth.Principal,
	tenantID uuid.UUID,
	obj Object,
	meta BucketMeta,
	action string,
) error {
	if h.policy == nil {
		return nil
	}
	decision, err := h.policy.IsAuthorized(ctx,
		apiutil.CedarPrincipal(principal),
		action,
		&cedar.Resource{
			TenantID:    tenantID,
			Collection:  obj.Collection,
			Key:         obj.Key,
			ObjectID:    obj.ObjectID,
			State:       string(obj.State),
			SizeBytes:   obj.SizeBytes,
			ContentType: obj.ContentType,
			Tags:        obj.Tags,
			BackendID:   meta.BackendID,
			BucketName:  meta.BucketName,
		},
		cedar.RequestContext{SizeBytes: obj.SizeBytes, ContentType: obj.ContentType, Now: time.Now()},
	)
	if err != nil {
		return apiutil.MapError(fmt.Errorf("authz: %w", err))
	}
	if decision != cedar.DecisionAllow {
		return connect.NewError(connect.CodePermissionDenied,
			fmt.Errorf("policy denied %s on %s/%s", action, obj.Collection, obj.Key))
	}
	return nil
}
