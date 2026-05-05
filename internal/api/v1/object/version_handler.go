package object

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/apiutil"
)

// VersionHandler implements the data-plane versioning RPCs. It is a sibling
// of *Handler — kept distinct because the versioning surface needs a
// separate VersionRepository, and not every deployment wires it (versioning
// is opt-in per bucket).
type VersionHandler struct {
	objects  Repository        // for parent Object lookups + collision checks
	versions VersionRepository // history + current pointer
}

func NewVersionHandler(objects Repository, versions VersionRepository) *VersionHandler {
	return &VersionHandler{objects: objects, versions: versions}
}

// ─── List ───────────────────────────────────────────────────────────────────

type ListVersionsInput struct {
	ParentName string // tenants/{t}/objectKeys/{ok}/objects/{id}
	PageSize   int32
	PageToken  string
}

func (h *VersionHandler) ListVersions(ctx context.Context, in ListVersionsInput) ([]ObjectVersion, string, error) {
	tenantID, _, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, "", err
	}
	objectKey, objectID, err := apiutil.ParseObjectName(in.ParentName)
	if err != nil {
		return nil, "", connect.NewError(connect.CodeInvalidArgument, err)
	}
	// Confirm the parent object exists + the caller's tenant owns it.
	if _, err := h.objects.FindByName(ctx, tenantID, objectKey, objectID.String()); err != nil {
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
	parent, err := h.objects.FindByName(ctx, tenantID, parsed.objectKey, parsed.objectID.String())
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	v, err := h.versions.Get(ctx, parsed.versionID)
	if err != nil {
		if errors.Is(err, ErrVersionNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
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
func (h *VersionHandler) RestoreVersion(ctx context.Context, name string) (*Object, error) {
	tenantID, _, err := apiutil.CallerContext(ctx)
	if err != nil {
		return nil, err
	}
	parsed, err := parseVersionName(name)
	if err != nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	parent, err := h.objects.FindByName(ctx, tenantID, parsed.objectKey, parsed.objectID.String())
	if err != nil {
		return nil, connect.NewError(connect.CodeNotFound, err)
	}
	v, err := h.versions.Get(ctx, parsed.versionID)
	if err != nil {
		if errors.Is(err, ErrVersionNotFound) {
			return nil, connect.NewError(connect.CodeNotFound, err)
		}
		return nil, connect.NewError(connect.CodeInternal, err)
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
	fresh, err := h.objects.FindByName(ctx, tenantID, parsed.objectKey, parsed.objectID.String())
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
	meta, err := h.objects.LookupBucketMeta(ctx, obj.TenantID, obj.ObjectKey)
	if err != nil {
		// Best-effort: a missing bucket-meta lookup must not abort the
		// already-successful promote. Log via the caller.
		return fmt.Errorf("on promote: lookup meta: %w", err)
	}
	if !meta.VersioningEnabled {
		return nil
	}
	return h.RecordPromotion(ctx, ObjectVersion{
		VersionID:    uuid.Must(uuid.NewV7()),
		ObjectID:     obj.ObjectID,
		S3Key:        obj.Key,
		SizeBytes:    obj.SizeBytes,
		ETag:         obj.ETag,
		ChecksumAlgo: obj.ChecksumAlgo,
		Checksum:     obj.Checksum,
		ContentType:  obj.ContentType,
		Metadata:     obj.Metadata,
		Tags:         obj.Tags,
	})
}

// UnsetDeleteMarkerCurrent walks the version history newest-first; if the
// current pointer points at a delete-marker, it flips to the most recent
// non-marker version. No-op when versioning is off, history is empty, or
// the current pointer already points at a regular version.
func (h *VersionHandler) UnsetDeleteMarkerCurrent(ctx context.Context, obj Object) error {
	if h == nil || h.objects == nil || h.versions == nil {
		return nil
	}
	meta, err := h.objects.LookupBucketMeta(ctx, obj.TenantID, obj.ObjectKey)
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
	meta, err := h.objects.LookupBucketMeta(ctx, obj.TenantID, obj.ObjectKey)
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
		S3Key:          obj.Key,
		ContentType:    obj.ContentType,
	})
}

// ─── helpers ────────────────────────────────────────────────────────────────

type versionNameParts struct {
	objectKey string
	objectID  uuid.UUID
	versionID uuid.UUID
}

// parseVersionName decodes
// "tenants/{t}/objectKeys/{ok}/objects/{id}/versions/{ver}" or the legacy
// short form "object_keys/{ok}/objects/{id}/versions/{ver}". Both forms are
// accepted for caller convenience; the handler operates on the trailing parts.
func parseVersionName(name string) (versionNameParts, error) {
	// Try the AIP-122 form first.
	const sep = "/versions/"
	idx := -1
	for i := 0; i+len(sep) <= len(name); i++ {
		if name[i:i+len(sep)] == sep {
			idx = i
			break
		}
	}
	if idx <= 0 {
		return versionNameParts{}, fmt.Errorf("invalid version name %q (missing /versions/{id})", name)
	}
	parentName := name[:idx]
	verIDStr := name[idx+len(sep):]
	verID, err := uuid.Parse(verIDStr)
	if err != nil {
		return versionNameParts{}, fmt.Errorf("invalid version_id: %w", err)
	}
	objectKey, objectID, err := parseObjectNameAny(parentName)
	if err != nil {
		return versionNameParts{}, err
	}
	return versionNameParts{objectKey: objectKey, objectID: objectID, versionID: verID}, nil
}

// parseObjectNameAny accepts either AIP-122 form
// "tenants/{t}/objectKeys/{ok}/objects/{id}" or the legacy short form
// "object_keys/{ok}/objects/{id}". Both feed the same handler logic.
func parseObjectNameAny(name string) (string, uuid.UUID, error) {
	if strings.HasPrefix(name, "tenants/") {
		parts := strings.Split(name, "/")
		if len(parts) == 6 && parts[2] == "objectKeys" && parts[4] == "objects" {
			id, err := uuid.Parse(parts[5])
			if err != nil {
				return "", uuid.Nil, fmt.Errorf("invalid object_id: %w", err)
			}
			return parts[3], id, nil
		}
	}
	return apiutil.ParseObjectName(name)
}
