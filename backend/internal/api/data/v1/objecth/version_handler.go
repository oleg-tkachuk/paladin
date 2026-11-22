package objecth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"uuid"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/backend/internal/rpcerr"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// VersionHandler implements the data-plane versioning RPCs. It is a sibling
// of *Handler — kept distinct because the versioning surface needs a
// separate VersionRepository, and not every deployment wires it (versioning
// is opt-in per bucket).
type VersionHandler struct {
	objects  Repository        // for parent Object lookups + collision checks
	versions VersionRepository // history + current pointer
	// locks is optional. When wired, a version promoted into a bucket with a
	// default retention inherits it — which is what makes the admin plane's
	// SetObjectLock mean anything. Without it the default is stored and never
	// applied, which is the state this field was added to end.
	locks LockRepository
	// policy authorises the versioning RPCs. Without it they refuse rather
	// than run unchecked — which is how they ran before it existed.
	policy cedar.Authorizer
	log    *zap.Logger
}

// SetLogger sets where each versioning RPC says how it ended. Unset, nothing
// is logged.
func (h *VersionHandler) SetLogger(l *zap.Logger) {
	if h != nil { // the wiring builds none where its repository is not wired
		h.log = l
	}
}

// errNoVersionAuthorizer is what a versioning RPC returns when the handler was
// built without an authorizer: refusing beats serving an unchecked call.
var errNoVersionAuthorizer = errors.New("versioning RPCs have no authorizer wired")

func NewVersionHandler(objects Repository, versions VersionRepository) *VersionHandler {
	return &VersionHandler{objects: objects, versions: versions}
}

// SetLockRepository wires the object-lock port after construction, matching
// how the object handler takes its optional collaborators. Nil is valid and
// means "this deployment does not do object lock".
func (h *VersionHandler) SetLockRepository(locks LockRepository) { h.locks = locks }

// SetAuthorizer wires the Cedar authorizer the versioning RPCs check against.
func (h *VersionHandler) SetAuthorizer(pe cedar.Authorizer) { h.policy = pe }

// authorizeParent applies the object RPCs' double gate to a version call on
// parent: the caller's capability (if any) must allow op on the object, and
// Cedar must allow action on it. Reading a version is reading the object;
// restoring one rewrites what the object is. rpc names the call in a warning.
func (h *VersionHandler) authorizeParent(
	ctx context.Context,
	rpc string,
	tenantID uuid.UUID,
	principal *auth.Principal,
	parent Object,
	op limes.Op,
	action cedar.Action,
) error {
	if err := auth.AssertCapabilityOp(ctx, op, CapabilityObjectURI(tenantID, parent.Collection, parent.Key)); err != nil {
		return err
	}
	if h.policy == nil {
		return connect.NewError(connect.CodePermissionDenied, errNoVersionAuthorizer.Error()).WithCause(errNoVersionAuthorizer)
	}
	backendID, bucket, err := h.parentBinding(ctx, rpc, tenantID, parent.Collection, action)
	if err != nil {
		return err
	}
	decision, err := h.policy.IsAuthorized(ctx,
		apiutil.CedarPrincipal(principal),
		action,
		&cedar.Resource{
			TenantID: tenantID, Collection: parent.Collection, Key: parent.Key,
			ObjectID: parent.ObjectID, State: string(parent.State),
			BackendID: backendID, BucketName: bucket,
			ContentType: parent.ContentType, SizeBytes: parent.SizeBytes, Tags: parent.Tags,
		},
		cedar.RequestContext{SizeBytes: parent.SizeBytes, ContentType: parent.ContentType, Now: time.Now()},
	)
	if err != nil {
		return apiutil.MapError(fmt.Errorf("authz: %w", err))
	}
	if decision != cedar.DecisionAllow {
		return connect.Errorf(connect.CodePermissionDenied,
			"policy denied %s on %s/%s", action, parent.Collection, parent.Key)
	}
	return nil
}

// parentBinding resolves the parent's (backend, bucket) for action's
// authorization. Reading a version is a read, best-effort as in GetObject: an
// unbound collection emits no bucket scope key, which leaves scoped principals
// fail-closed. Anything else rewrites the object, so it goes through the write
// gate and fails closed.
func (h *VersionHandler) parentBinding(ctx context.Context, rpc string, tenantID uuid.UUID, collection string, action cedar.Action) (backendID, bucket string, err error) {
	if action == cedar.ActionGetObject {
		backendID, bucket = scopeBucket(ctx, h.log, h.objects, rpc, tenantID, collection)
		return backendID, bucket, nil
	}
	return writeBinding(ctx, h.objects, tenantID, collection, bucketgate.BytesWrite)
}

// CapabilityObjectURI is the resource a capability names an object by.
func CapabilityObjectURI(tenantID uuid.UUID, collection, key string) string {
	return capabilityCollectionURI(tenantID, collection) + key
}

// capabilityObjectURIPrefix starts every object URI a capability names.
const capabilityObjectURIPrefix = paladin.ObjectResourceScheme

// parseCapabilityObjectURI splits "object://<tenant>/<collection>/<key>".
// The key may itself contain "/"; the tenant and collection may not.
func parseCapabilityObjectURI(uri string) (tenantID uuid.UUID, collection, key string, ok bool) {
	rest, found := strings.CutPrefix(uri, capabilityObjectURIPrefix)
	if !found {
		return uuid.Nil(), "", "", false
	}
	tenant, rest, found := strings.Cut(rest, "/")
	if !found {
		return uuid.Nil(), "", "", false
	}
	collection, key, found = strings.Cut(rest, "/")
	if !found || collection == "" || key == "" {
		return uuid.Nil(), "", "", false
	}
	tenantID, err := uuid.Parse(tenant)
	if err != nil {
		return uuid.Nil(), "", "", false
	}
	return tenantID, collection, key, true
}

// capabilityCollectionURI is the URI prefix every object in a collection
// shares — the resource an operation over the whole collection asserts.
func capabilityCollectionURI(tenantID uuid.UUID, collection string) string {
	return capabilityObjectURIPrefix + tenantID.String() + "/" + collection + "/"
}

// ─── List ───────────────────────────────────────────────────────────────────

type ListVersionsInput struct {
	Collection string
	ObjectID   string
	PageSize   int32
	PageToken  string
}

func (h *VersionHandler) ListVersions(ctx context.Context, in ListVersionsInput) (_ []ObjectVersion, _ string, err error) {
	defer func() {
		logOutcome(ctx, h.log, opListVersions, zapcore.DebugLevel, err, objectFields(in.Collection, in.ObjectID)...)
	}()
	tenantID, principal, err := apiutil.ActingContext(ctx)
	if err != nil {
		return nil, "", err
	}
	if in.Collection == "" || in.ObjectID == "" {
		return nil, "", connect.NewError(connect.CodeInvalidArgument, "collection and object_id are required")
	}
	objectID, err := uuid.Parse(in.ObjectID)
	if err != nil {
		return nil, "", rpcerr.New(connect.CodeInvalidArgument, fmt.Errorf("invalid object_id: %w", err))
	}
	// Confirm the parent object exists + the caller's tenant owns it.
	parent, err := h.objects.FindByName(ctx, tenantID, in.Collection, in.ObjectID)
	if err != nil {
		return nil, "", objectLookupError(err)
	}
	if err := h.authorizeParent(ctx, opListVersions, tenantID, principal, parent, limes.OpGet, cedar.ActionGetObject); err != nil {
		return nil, "", err
	}
	out, next, err := h.versions.List(ctx, objectID, in.PageSize, in.PageToken)
	if err != nil {
		return nil, "", listError(err)
	}
	return out, next, nil
}

// ─── Get ────────────────────────────────────────────────────────────────────

func (h *VersionHandler) GetVersion(ctx context.Context, name string) (_ *ObjectVersion, err error) {
	defer func() { logOutcome(ctx, h.log, opGetVersion, zapcore.DebugLevel, err, zap.String("version", name)) }()
	tenantID, principal, err := apiutil.ActingContext(ctx)
	if err != nil {
		return nil, err
	}
	parsed, err := parseVersionName(name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	// Tenant guard via parent Object lookup.
	parent, err := h.objects.FindByName(ctx, tenantID, parsed.collection, parsed.objectID.String())
	if err != nil {
		return nil, objectLookupError(err)
	}
	if err := h.authorizeParent(ctx, opGetVersion, tenantID, principal, parent, limes.OpGet, cedar.ActionGetObject); err != nil {
		return nil, err
	}
	v, err := h.versionOf(ctx, parsed.versionID, parent)
	if err != nil {
		return nil, err
	}
	// Not best-effort: an unread pointer would answer is_current wrongly.
	current, err := h.versions.CurrentVersionID(ctx, parent.ObjectID)
	if err != nil {
		return nil, rpcerr.New(connect.CodeInternal, fmt.Errorf("read current version: %w", err))
	}
	if v.VersionID == current {
		v.IsCurrent = true
	}
	return &v, nil
}

// ─── Restore ────────────────────────────────────────────────────────────────

// RestoreVersion makes the named version the current one. If the named
// version is a delete marker, the operation fails — clients should use
// RestoreObject in that case (which clears the most recent delete marker).
//
// resourceVersion is the OCC guard on the PARENT object, not on the version
// being restored: restoring repoints the parent's current-version pointer, so
// that is the row a concurrent writer would be racing for. It is mandatory.
// The field existed on the request for a while but was never read here, so a
// console that dutifully sent it got no protection from it.
func (h *VersionHandler) RestoreVersion(ctx context.Context, name, resourceVersion string) (_ *Object, err error) {
	defer func() { logOutcome(ctx, h.log, opRestoreVersion, zapcore.InfoLevel, err, zap.String("version", name)) }()
	// Shape of the request first: this needs no identity and no lookup, and
	// checking it here rather than trusting protovalidate means callers that
	// reach the handler by another route get the same guarantee.
	expected, err := requiredResourceVersion(resourceVersion)
	if err != nil {
		return nil, err
	}
	tenantID, principal, err := apiutil.ActingContext(ctx)
	if err != nil {
		return nil, err
	}
	parsed, err := parseVersionName(name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err.Error()).WithCause(err)
	}
	parent, err := h.objects.FindByName(ctx, tenantID, parsed.collection, parsed.objectID.String())
	if err != nil {
		return nil, objectLookupError(err)
	}
	if err := h.authorizeParent(ctx, opRestoreVersion, tenantID, principal, parent, limes.OpPut, cedar.ActionPutObject); err != nil {
		return nil, err
	}
	if expected != parent.ResourceVersion {
		return nil, connect.Errorf(connect.CodeAborted,
			"resource_version mismatch: expected %d, current %d",
			expected, parent.ResourceVersion)
	}
	v, err := h.versionOf(ctx, parsed.versionID, parent)
	if err != nil {
		return nil, err
	}
	if v.IsDeleteMarker {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			"cannot restore a delete marker; use RestoreObject")
	}
	return h.makeCurrent(ctx, tenantID, parsed, v)
}

// msgVersionNotOfObject refuses a version name whose version belongs to
// another object than the one it names.
const msgVersionNotOfObject = "version does not belong to the named object"

// versionOf reads the version versionID of parent; NotFound when there is
// none, or it is another object's.
func (h *VersionHandler) versionOf(ctx context.Context, versionID uuid.UUID, parent Object) (ObjectVersion, error) {
	v, err := h.versions.Get(ctx, versionID)
	if err != nil {
		return ObjectVersion{}, apiutil.MapError(err)
	}
	if v.ObjectID != parent.ObjectID {
		return ObjectVersion{}, connect.NewError(connect.CodeNotFound, msgVersionNotOfObject)
	}
	return v, nil
}

// makeCurrent repoints the object at v and reads it back, so callers see
// authoritative state.
func (h *VersionHandler) makeCurrent(ctx context.Context, tenantID uuid.UUID, parsed versionNameParts, v ObjectVersion) (*Object, error) {
	if err := h.versions.SetCurrentVersionID(ctx, v.ObjectID, v.VersionID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	fresh, err := h.objects.FindByName(ctx, tenantID, parsed.collection, parsed.objectID.String())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return &fresh, nil
}

// ─── Insert (used by promotion paths) ───────────────────────────────────────
//
// RecordPromotion is called by callers that have just promoted an object to
// AVAILABLE and need a versions-row written. Bucket-versioning gating
// happens at the caller layer (only call when the parent bucket has
// versioning_enabled).
func (h *VersionHandler) RecordPromotion(ctx context.Context, v ObjectVersion) error {
	if v.ObjectID == uuid.Nil() {
		return errors.New("RecordPromotion: ObjectID required")
	}
	if v.CreatedAt.IsZero() {
		v.CreatedAt = time.Now().UTC()
	}
	if err := h.versions.Insert(ctx, v); err != nil {
		return fmt.Errorf("insert version: %w", err)
	}
	return h.versions.SetCurrentVersionID(ctx, v.ObjectID, v.VersionID)
}

// wired reports whether the version hooks have what they need; a deployment
// without versioning builds none.
func (h *VersionHandler) wired() bool {
	return h != nil && h.objects != nil && h.versions != nil
}

// OnPromote is the hook the object handler calls after a successful state
// transition to AVAILABLE. Looks up the parent bucket's versioning flag via
// the shared object Repository; when enabled, records a new version row +
// flips the current pointer. No-op when versioning is off — keeps callers
// free of the policy decision.
func (h *VersionHandler) OnPromote(ctx context.Context, obj Object) error {
	if !h.wired() {
		return nil
	}
	meta, err := h.objects.LookupBucketMeta(ctx, obj.TenantID, obj.Collection, bucketgate.Read) // versioning-config read; primary op already gated
	if err != nil {
		// Best-effort: a missing bucket-meta lookup must not abort the
		// already-successful promote. Log via the caller.
		return fmt.Errorf("on promote: lookup meta: %w", err)
	}
	if !meta.VersioningEnabled {
		return nil
	}
	versionID := uuid.NewV7()
	if err := h.RecordPromotion(ctx, ObjectVersion{
		VersionID:             versionID,
		ObjectID:              obj.ObjectID,
		StoragePath:           obj.Key,
		SizeBytes:             obj.SizeBytes,
		ETag:                  obj.ETag,
		ChecksumAlgo:          obj.ChecksumAlgo,
		Checksum:              obj.Checksum,
		ChecksumPartSizeBytes: obj.ChecksumPartSizeBytes,
		ContentType:           obj.ContentType,
		Metadata:              obj.Metadata,
		Tags:                  obj.Tags,
	}); err != nil {
		return err
	}

	// A bucket-level default retention applies to every version written into
	// the bucket, which is the only thing that makes it a default rather than
	// a note. ApplyBucketDefault never overwrites an existing row, so an
	// explicit SetObjectRetention that raced ahead of this still wins.
	//
	// The failure is deliberately not fatal to the promotion: the object is
	// already AVAILABLE in storage and in the objects row by the time this
	// runs, and refusing to acknowledge that would be a lie. The caller logs
	// what comes back.
	return h.applyDefaultLock(ctx, obj.TenantID, versionID, meta)
}

// applyDefaultLock puts a new version under its bucket's default retention,
// where the bucket has one and this deployment does object lock.
func (h *VersionHandler) applyDefaultLock(ctx context.Context, tenantID, versionID uuid.UUID, meta BucketMeta) error {
	if h.locks == nil || !meta.ObjectLockEnabled || meta.ObjectLockDefaultMode == "" {
		return nil
	}
	if err := h.locks.ApplyBucketDefault(ctx, tenantID, versionID,
		meta.ObjectLockDefaultMode, meta.ObjectLockDefaultRetention); err != nil {
		return fmt.Errorf("apply bucket default lock: %w", err)
	}
	return nil
}

// UnsetDeleteMarkerCurrent walks the version history newest-first; if the
// current pointer points at a delete-marker, it flips to the most recent
// non-marker version. No-op when versioning is off, history is empty, or
// the current pointer already points at a regular version.
func (h *VersionHandler) UnsetDeleteMarkerCurrent(ctx context.Context, obj Object) error {
	if !h.wired() {
		return nil
	}
	// None of these reads is best-effort: a restore that went on without one
	// would leave a visible object whose current version is its delete
	// marker, and nothing moves the pointer back later.
	meta, err := h.objects.LookupBucketMeta(ctx, obj.TenantID, obj.Collection, bucketgate.Read) // versioning-config read; primary op already gated
	if err != nil {
		return fmt.Errorf("read versioning config: %w", err)
	}
	if !meta.VersioningEnabled {
		return nil
	}
	markerID, ok, err := h.currentDeleteMarker(ctx, obj.ObjectID)
	if err != nil || !ok {
		return err
	}
	// Walk history (newest first, skip the marker) and pick the first
	// non-marker entry, scanning at most markerScanLimit versions on a
	// pathological object with many delete markers.
	page, _, err := h.versions.List(ctx, obj.ObjectID, markerScanLimit, "")
	if err != nil {
		return err
	}
	// No non-marker history found in the first page clears the pointer: the
	// object becomes AVAILABLE but with no current version (legacy shape).
	return h.versions.SetCurrentVersionID(ctx, obj.ObjectID, latestNonMarker(page, markerID))
}

// markerScanLimit is the most versions a restore scans for one that is not a
// delete marker.
const markerScanLimit = 100

// currentDeleteMarker is the object's current version when that is a delete
// marker. No pointer, or one naming a version that is not there, is no
// marker; a read that fails is the error.
func (h *VersionHandler) currentDeleteMarker(ctx context.Context, objectID uuid.UUID) (uuid.UUID, bool, error) {
	curID, err := h.versions.CurrentVersionID(ctx, objectID)
	if err != nil {
		return uuid.Nil(), false, fmt.Errorf("read current version: %w", err)
	}
	if curID == uuid.Nil() {
		return uuid.Nil(), false, nil
	}
	cur, err := h.versions.Get(ctx, curID)
	switch {
	case errors.Is(err, ErrVersionNotFound):
		return uuid.Nil(), false, nil
	case err != nil:
		return uuid.Nil(), false, fmt.Errorf("read current version %s: %w", curID, err)
	}
	return curID, cur.IsDeleteMarker, nil
}

// latestNonMarker is the newest version of page, newest first, that is
// neither markerID nor a delete marker; uuid.Nil when there is none.
func latestNonMarker(page []ObjectVersion, markerID uuid.UUID) uuid.UUID {
	for _, v := range page {
		if v.VersionID != markerID && !v.IsDeleteMarker {
			return v.VersionID
		}
	}
	return uuid.Nil()
}

// OnSoftDelete records a delete-marker version when the parent bucket has
// versioning enabled. Combined with the soft-delete state transition, this
// preserves the AVAILABLE history while hiding the object from default reads.
func (h *VersionHandler) OnSoftDelete(ctx context.Context, obj Object) error {
	if !h.wired() {
		return nil
	}
	meta, err := h.objects.LookupBucketMeta(ctx, obj.TenantID, obj.Collection, bucketgate.Read) // versioning-config read; primary op already gated
	if err != nil {
		return fmt.Errorf("on soft delete: lookup meta: %w", err)
	}
	if !meta.VersioningEnabled {
		return nil
	}
	return h.RecordPromotion(ctx, ObjectVersion{
		VersionID:      uuid.NewV7(),
		ObjectID:       obj.ObjectID,
		IsDeleteMarker: true,
		StoragePath:    obj.Key,
		ContentType:    obj.ContentType,
	})
}

// ─── helpers ────────────────────────────────────────────────────────────────

type versionNameParts struct {
	collection string
	objectID   uuid.UUID
	versionID  uuid.UUID
}

// parseVersionName decodes "tenants/{t}/collections/{collection}/objects/{id}/versions/{ver}"
// with the Go SDK's parser, which the server and both SDKs share
// (sdk/testdata/names.json). The collection may contain '/'.
func parseVersionName(name string) (versionNameParts, error) {
	n, err := paladin.ParseObjectVersionName(name)
	if err != nil {
		return versionNameParts{}, err
	}
	objectID, err := uuid.Parse(n.Object)
	if err != nil {
		return versionNameParts{}, err
	}
	versionID, err := uuid.Parse(n.Version)
	if err != nil {
		return versionNameParts{}, err
	}
	return versionNameParts{collection: n.Collection, objectID: objectID, versionID: versionID}, nil
}
