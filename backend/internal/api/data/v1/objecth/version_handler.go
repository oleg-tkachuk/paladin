package objecth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
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
}

func NewVersionHandler(objects Repository, versions VersionRepository) *VersionHandler {
	return &VersionHandler{objects: objects, versions: versions}
}

// SetLockRepository wires the object-lock port after construction, matching
// how the object handler takes its optional collaborators. Nil is valid and
// means "this deployment does not do object lock".
func (h *VersionHandler) SetLockRepository(locks LockRepository) { h.locks = locks }

// ─── List ───────────────────────────────────────────────────────────────────

type ListVersionsInput struct {
	Collection string
	ObjectID   string
	PageSize   int32
	PageToken  string
}

func (h *VersionHandler) ListVersions(ctx context.Context, in ListVersionsInput) ([]ObjectVersion, string, error) {
	tenantID, _, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, "", err
	}
	if in.Collection == "" || in.ObjectID == "" {
		return nil, "", connect.NewError(connect.CodeInvalidArgument, errors.New("collection and object_id are required"))
	}
	objectID, err := uuid.Parse(in.ObjectID)
	if err != nil {
		return nil, "", connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("invalid object_id: %w", err))
	}
	// Confirm the parent object exists + the caller's tenant owns it.
	if _, err := h.objects.FindByName(ctx, tenantID, in.Collection, in.ObjectID); err != nil {
		return nil, "", connect.NewError(connect.CodeNotFound, err)
	}
	out, next, err := h.versions.List(ctx, objectID, in.PageSize, in.PageToken)
	if err != nil {
		return nil, "", connect.NewError(connect.CodeInternal, err)
	}
	return out, next, nil
}

// ─── Get ────────────────────────────────────────────────────────────────────

func (h *VersionHandler) GetVersion(ctx context.Context, name string) (*ObjectVersion, error) {
	tenantID, _, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, err
	}
	parsed, err := parseVersionName(name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	// Tenant guard via parent Object lookup.
	parent, err := h.objects.FindByName(ctx, tenantID, parsed.collection, parsed.objectID.String())
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	v, err := h.versions.Get(ctx, parsed.versionID)
	if err != nil {
		return nil, apiutil.MapError(err)
	}
	if v.ObjectID != parent.ObjectID {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("version does not belong to the named object"))
	}
	current, _ := h.versions.CurrentVersionID(ctx, parent.ObjectID)
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
func (h *VersionHandler) RestoreVersion(ctx context.Context, name, resourceVersion string) (*Object, error) {
	// Shape of the request first: this needs no identity and no lookup, and
	// checking it here rather than trusting protovalidate means callers that
	// reach the handler by another route get the same guarantee.
	if resourceVersion == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			errors.New("resource_version is required"))
	}
	expected, err := parseInt64(resourceVersion)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("invalid resource_version: %w", err))
	}
	tenantID, _, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, err
	}
	parsed, err := parseVersionName(name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	parent, err := h.objects.FindByName(ctx, tenantID, parsed.collection, parsed.objectID.String())
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	if expected != parent.ResourceVersion {
		return nil, connect.NewError(connect.CodeAborted,
			fmt.Errorf("resource_version mismatch: expected %d, current %d",
				expected, parent.ResourceVersion))
	}
	v, err := h.versions.Get(ctx, parsed.versionID)
	if err != nil {
		return nil, apiutil.MapError(err)
	}
	if v.ObjectID != parent.ObjectID {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("version does not belong to the named object"))
	}
	if v.IsDeleteMarker {
		return nil, connect.NewError(connect.CodeFailedPrecondition,
			errors.New("cannot restore a delete marker; use RestoreObject"))
	}
	if err := h.versions.SetCurrentVersionID(ctx, parent.ObjectID, v.VersionID); err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	// Refresh the parent Object envelope so callers see authoritative state.
	fresh, err := h.objects.FindByName(ctx, tenantID, parsed.collection, parsed.objectID.String())
	if err != nil {
		return nil, connect.NewError(connect.CodeInternal, err)
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
	if v.ObjectID == uuid.Nil {
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

// OnPromote is the hook the object handler calls after a successful state
// transition to AVAILABLE. Looks up the parent bucket's versioning flag via
// the shared object Repository; when enabled, records a new version row +
// flips the current pointer. No-op when versioning is off — keeps callers
// free of the policy decision.
func (h *VersionHandler) OnPromote(ctx context.Context, obj Object) error {
	if h == nil || h.objects == nil || h.versions == nil {
		return nil
	}
	meta, err := h.objects.LookupBucketMeta(ctx, obj.TenantID, obj.Collection, false) // versioning-config read; primary op already gated
	if err != nil {
		// Best-effort: a missing bucket-meta lookup must not abort the
		// already-successful promote. Log via the caller.
		return fmt.Errorf("on promote: lookup meta: %w", err)
	}
	if !meta.VersioningEnabled {
		return nil
	}
	versionID := uuid.Must(uuid.NewV7())
	if err := h.RecordPromotion(ctx, ObjectVersion{
		VersionID:    versionID,
		ObjectID:     obj.ObjectID,
		StoragePath:  obj.Key,
		SizeBytes:    obj.SizeBytes,
		ETag:         obj.ETag,
		ChecksumAlgo: obj.ChecksumAlgo,
		Checksum:     obj.Checksum,
		ContentType:  obj.ContentType,
		Metadata:     obj.Metadata,
		Tags:         obj.Tags,
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
	if h.locks != nil && meta.ObjectLockEnabled && meta.ObjectLockDefaultMode != "" {
		if err := h.locks.ApplyBucketDefault(ctx, obj.TenantID, versionID,
			meta.ObjectLockDefaultMode, meta.ObjectLockDefaultRetention); err != nil {
			return fmt.Errorf("apply bucket default lock: %w", err)
		}
	}
	return nil
}

// UnsetDeleteMarkerCurrent walks the version history newest-first; if the
// current pointer points at a delete-marker, it flips to the most recent
// non-marker version. No-op when versioning is off, history is empty, or
// the current pointer already points at a regular version.
func (h *VersionHandler) UnsetDeleteMarkerCurrent(ctx context.Context, obj Object) error {
	if h == nil || h.objects == nil || h.versions == nil {
		return nil
	}
	meta, err := h.objects.LookupBucketMeta(ctx, obj.TenantID, obj.Collection, false) // versioning-config read; primary op already gated
	if err != nil {
		// Don't block restore on a meta lookup failure — the state machine
		// will still flip the row visible. Surfaces as a Warning at the call
		// site if needed.
		return nil
	}
	if !meta.VersioningEnabled {
		return nil
	}
	curID, err := h.versions.CurrentVersionID(ctx, obj.ObjectID)
	if err != nil || curID == uuid.Nil {
		return nil
	}
	cur, err := h.versions.Get(ctx, curID)
	if err != nil {
		return nil
	}
	if !cur.IsDeleteMarker {
		return nil
	}
	// Walk history (newest first, skip the marker) and pick the first
	// non-marker entry. Hard cap at 100 to avoid unbounded scans on a
	// pathological object with many delete markers.
	page, _, err := h.versions.List(ctx, obj.ObjectID, 100, "")
	if err != nil {
		return err
	}
	for _, v := range page {
		if v.VersionID == curID {
			continue
		}
		if v.IsDeleteMarker {
			continue
		}
		return h.versions.SetCurrentVersionID(ctx, obj.ObjectID, v.VersionID)
	}
	// No non-marker history found in the first page — clear pointer. The
	// object becomes AVAILABLE but with no current version (legacy shape).
	return h.versions.SetCurrentVersionID(ctx, obj.ObjectID, uuid.Nil)
}

// OnSoftDelete records a delete-marker version when the parent bucket has
// versioning enabled. Combined with the soft-delete state transition, this
// preserves the AVAILABLE history while hiding the object from default reads.
func (h *VersionHandler) OnSoftDelete(ctx context.Context, obj Object) error {
	if h == nil || h.objects == nil || h.versions == nil {
		return nil
	}
	meta, err := h.objects.LookupBucketMeta(ctx, obj.TenantID, obj.Collection, false) // versioning-config read; primary op already gated
	if err != nil {
		return fmt.Errorf("on soft delete: lookup meta: %w", err)
	}
	if !meta.VersioningEnabled {
		return nil
	}
	return h.RecordPromotion(ctx, ObjectVersion{
		VersionID:      uuid.Must(uuid.NewV7()),
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

// parseVersionName decodes the AIP-122 form
// "tenants/{t}/collections/{ok}/objects/{id}/versions/{ver}".
//
// The collection body may contain slashes — "e2e/7f3fec78" is an ordinary
// collection name, and every e2e fixture uses that shape. Splitting on "/" and
// demanding six segments therefore rejected legitimate names, which made the
// version RPCs unreachable for them. Locate the separators instead, and let
// the collection be whatever sits between them; this mirrors objectNameParts
// in connectshim/data, which parses the same form minus the version suffix.
func parseVersionName(name string) (versionNameParts, error) {
	const (
		prefix = "tenants/"
		okSep  = "/collections/"
		objSep = "/objects/"
		verSep = "/versions/"
	)
	if !strings.HasPrefix(name, prefix) {
		return versionNameParts{}, fmt.Errorf("invalid version name %q (must start with %q)", name, prefix)
	}
	// Last separator in each case, so the same token appearing inside the
	// collection body cannot shadow the real suffix.
	verIdx := strings.LastIndex(name, verSep)
	if verIdx <= 0 {
		return versionNameParts{}, fmt.Errorf("invalid version name %q (missing /versions/{id})", name)
	}
	verIDStr := name[verIdx+len(verSep):]
	verID, err := uuid.Parse(verIDStr)
	if err != nil {
		return versionNameParts{}, fmt.Errorf("invalid version_id: %w", err)
	}

	rest := name[len(prefix):verIdx]
	okIdx := strings.Index(rest, okSep)
	if okIdx <= 0 {
		return versionNameParts{}, fmt.Errorf("invalid version name %q (missing %q)", name, okSep)
	}
	if _, err := uuid.Parse(rest[:okIdx]); err != nil {
		return versionNameParts{}, fmt.Errorf("invalid tenant_id in name: %w", err)
	}

	afterOK := rest[okIdx+len(okSep):]
	objIdx := strings.LastIndex(afterOK, objSep)
	if objIdx <= 0 {
		return versionNameParts{}, fmt.Errorf("invalid version name %q (missing %q)", name, objSep)
	}
	collection := afterOK[:objIdx]
	objIDStr := afterOK[objIdx+len(objSep):]
	if collection == "" {
		return versionNameParts{}, fmt.Errorf("invalid version name %q (empty collection)", name)
	}
	// The object id is one segment; a slash here means the caller's
	// "/objects/" landed inside the collection body rather than before the id.
	if strings.Contains(objIDStr, "/") {
		return versionNameParts{}, fmt.Errorf("invalid version name %q (object_id must be one segment)", name)
	}
	objectID, err := uuid.Parse(objIDStr)
	if err != nil {
		return versionNameParts{}, fmt.Errorf("invalid object_id: %w", err)
	}
	return versionNameParts{collection: collection, objectID: objectID, versionID: verID}, nil
}
