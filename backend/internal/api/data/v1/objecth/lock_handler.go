package objecth

import (
	"context"
	"errors"
	"fmt"
	"time"
	"uuid"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/metrics"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
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
	log      *zap.Logger
}

// SetLogger sets where each lock RPC says how it ended. Unset, nothing is
// logged.
func (h *LockHandler) SetLogger(l *zap.Logger) {
	if h != nil { // the wiring builds none where its repository is not wired
		h.log = l
	}
}

// The object-lock metric's op and outcome labels.
const (
	lockMetricRetention = "retention"
	lockMetricLegalHold = "legal_hold"

	lockOutcomeApplied           = "applied"
	lockOutcomeAppliedWithBypass = "applied_with_bypass"
	lockOutcomeRefusedWeakening  = "refused_weakening"
	lockOutcomeError             = "error"
	lockOutcomePlaced            = "placed"
	lockOutcomeReleased          = "released"
)

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
func (h *LockHandler) SetRetention(ctx context.Context, in SetRetentionInput) (_ ObjectLock, err error) {
	defer func() {
		logOutcome(ctx, h.log, opSetRetention, zapcore.InfoLevel, err,
			append(objectFields(in.Collection, in.ObjectID), zap.String("mode", in.Mode),
				zap.Bool("bypass_governance", in.BypassGovernance))...)
	}()
	if err := checkRetention(in); err != nil {
		return ObjectLock{}, err
	}

	res, err := h.resolve(ctx, in.Collection, in.ObjectID, cedar.ActionSetObjectRetention)
	if err != nil {
		return ObjectLock{}, err
	}
	if !res.meta.ObjectLockEnabled {
		return ObjectLock{}, connect.NewError(connect.CodeFailedPrecondition, ErrObjectLockNotEnabled.Error()).WithCause(ErrObjectLockNotEnabled)
	}

	// The bypass flag is a request to weaken a control, so it is gated on a
	// role rather than on the Cedar action that got us here — the same gate
	// DeleteObject uses, and for the same reason.
	if in.BypassGovernance && !mayBypassGovernance(res.principal) {
		return ObjectLock{}, connect.NewError(connect.CodePermissionDenied, msgBypassNeedsRole)
	}

	return h.writeRetention(ctx, res, in)
}

// checkRetention refuses a retention that is not GOVERNANCE or COMPLIANCE,
// or whose window has already closed: such a window asserts nothing but
// leaves a row implying it does, and refusing is clearer than storing a
// no-op.
func checkRetention(in SetRetentionInput) error {
	if in.Mode != LockModeGovernance && in.Mode != LockModeCompliance {
		return connect.Errorf(connect.CodeInvalidArgument,
			"mode must be %s or %s, got %q", LockModeGovernance, LockModeCompliance, in.Mode)
	}
	if !in.RetainUntil.After(time.Now()) {
		return connect.NewError(connect.CodeInvalidArgument,
			"retain_until must be in the future")
	}
	return nil
}

// writeRetention stores an authorized retention on the resolved version and
// counts how it went.
func (h *LockHandler) writeRetention(ctx context.Context, res resolved, in SetRetentionInput) (ObjectLock, error) {
	lock, err := h.locks.SetRetention(ctx, SetRetentionArgs{
		TenantID:         res.tenantID,
		VersionID:        res.versionID,
		Mode:             in.Mode,
		RetainUntil:      in.RetainUntil,
		BypassGovernance: in.BypassGovernance,
	})
	if errors.Is(err, ErrRetentionWeakened) {
		metrics.RecordObjectLock(ctx, lockMetricRetention, in.Mode, lockOutcomeRefusedWeakening)
		return ObjectLock{}, connect.NewError(connect.CodeFailedPrecondition, err.Error()).WithCause(err)
	}
	if err != nil {
		metrics.RecordObjectLock(ctx, lockMetricRetention, in.Mode, lockOutcomeError)
		return ObjectLock{}, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	// A COMPLIANCE window cannot be shortened by anyone, so every one of these
	// is an irreversible commitment of storage until its date passes. Counting
	// them is the only way an operator learns how much of that has accumulated
	// before the bill or the capacity alert does the telling.
	outcome := lockOutcomeApplied
	if in.BypassGovernance {
		outcome = lockOutcomeAppliedWithBypass
	}
	metrics.RecordObjectLock(ctx, lockMetricRetention, in.Mode, outcome)
	return lock, nil
}

// SetLegalHold turns a legal hold on or off on the object's current version.
func (h *LockHandler) SetLegalHold(ctx context.Context, collection, objectID string, hold bool) (_ ObjectLock, err error) {
	defer func() {
		logOutcome(ctx, h.log, opSetLegalHold, zapcore.InfoLevel, err,
			append(objectFields(collection, objectID), zap.Bool("hold", hold))...)
	}()
	res, err := h.resolve(ctx, collection, objectID, cedar.ActionSetObjectLegalHold)
	if err != nil {
		return ObjectLock{}, err
	}
	// Placing a hold requires the bucket to have opted in, for the same
	// reason retention does. Releasing one does not: a bucket whose lock
	// setting was turned off afterwards must still be releasable, or the hold
	// outlives every way of lifting it.
	if hold && !res.meta.ObjectLockEnabled {
		return ObjectLock{}, connect.NewError(connect.CodeFailedPrecondition, ErrObjectLockNotEnabled.Error()).WithCause(ErrObjectLockNotEnabled)
	}

	lock, err := h.locks.SetLegalHold(ctx, res.tenantID, res.versionID, hold)
	if err != nil {
		metrics.RecordObjectLock(ctx, lockMetricLegalHold, "", lockOutcomeError)
		return ObjectLock{}, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	outcome := lockOutcomeReleased
	if hold {
		outcome = lockOutcomePlaced
	}
	metrics.RecordObjectLock(ctx, lockMetricLegalHold, "", outcome)
	return lock, nil
}

// GetLock reads the lock on the object's current version.
func (h *LockHandler) GetLock(ctx context.Context, collection, objectID string) (_ ObjectLock, err error) {
	defer func() {
		logOutcome(ctx, h.log, opGetLock, zapcore.DebugLevel, err, objectFields(collection, objectID)...)
	}()
	res, err := h.resolve(ctx, collection, objectID, cedar.ActionReadObjectLock)
	if err != nil {
		return ObjectLock{}, err
	}
	lock, err := h.locks.GetByVersion(ctx, res.versionID)
	if err != nil {
		return ObjectLock{}, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
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
func (h *LockHandler) resolve(ctx context.Context, collection, objectID string, action cedar.Action) (resolved, error) {
	var out resolved
	tenantID, principal, err := apiutil.ActingContext(ctx)
	if err != nil {
		return out, err
	}
	if err := requireObjectName(collection, objectID); err != nil {
		return out, err
	}
	obj, err := h.objects.FindByName(ctx, tenantID, collection, objectID)
	if err != nil {
		return out, objectLookupError(err)
	}
	// Setting a lock writes only its row (object_locks), so a draining
	// backend takes it as it serves a read of one.
	meta, err := h.objects.LookupBucketMeta(ctx, tenantID, collection, lockAccess(action))
	if err != nil {
		return out, MapResolveErr(err)
	}
	if err := auth.AssertCapabilityOp(ctx, limes.OpManage, ""); err != nil {
		return out, err
	}
	if err := h.authorizeLock(ctx, principal, tenantID, obj, meta, action); err != nil {
		return out, err
	}

	versionID, err := h.lockedVersion(ctx, obj.ObjectID)
	if err != nil {
		return out, err
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

// lockAccess is what a lock RPC touches: reading a lock is a read, and
// anything else sets one, which writes the object_locks row and nothing on
// the backend.
func lockAccess(action cedar.Action) bucketgate.Access {
	if action == cedar.ActionReadObjectLock {
		return bucketgate.Read
	}
	return bucketgate.MetadataWrite
}

// lockedVersion is the version a lock on the object attaches to: its current
// one. Every promoted object gets a version row, so an object without one is
// still PENDING or was written before versioning existed. Either way there
// is nothing to attach a lock to, and silently succeeding would report a lock
// that protects nothing.
func (h *LockHandler) lockedVersion(ctx context.Context, objectID uuid.UUID) (uuid.UUID, error) {
	versionID, err := h.versions.CurrentVersionID(ctx, objectID)
	if err != nil {
		return uuid.Nil(), connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	if versionID == uuid.Nil() {
		return uuid.Nil(), connect.NewError(connect.CodeFailedPrecondition, ErrNoCurrentVersion.Error()).WithCause(ErrNoCurrentVersion)
	}
	return versionID, nil
}

func (h *LockHandler) authorizeLock(
	ctx context.Context,
	principal *auth.Principal,
	tenantID uuid.UUID,
	obj Object,
	meta BucketMeta,
	action cedar.Action,
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
		return connect.Errorf(connect.CodePermissionDenied,
			"policy denied %s on %s/%s", action, obj.Collection, obj.Key)
	}
	apiutil.AdmitTenant(ctx, tenantID)
	return nil
}
