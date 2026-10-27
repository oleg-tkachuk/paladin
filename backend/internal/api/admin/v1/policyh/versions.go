package policyh

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/rpcerr"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// Layer is the kind of row a Cedar policy is stored on.
type Layer int

const (
	LayerStorageBackend Layer = iota + 1
	LayerBucket
	LayerCollection
	LayerTenant
)

// LayerRef names one stored policy layer.
type LayerRef struct {
	Layer Layer
	// TenantID is the tenant a collection or tenant layer belongs to.
	TenantID uuid.UUID
	// Collection is the collection's name, for LayerCollection.
	Collection string
	// BackendID is the storage backend, for LayerStorageBackend and
	// LayerBucket.
	BackendID uuid.UUID
	// Bucket is the bucket's name, for LayerBucket.
	Bucket string
}

// Version is one text a layer has held.
type Version struct {
	Version     int32
	CedarPolicy string
	// WrittenBy is the subject of the principal whose request wrote it; nil
	// when no request did (a migration, an operator's psql).
	WrittenBy *string
	WrittenAt time.Time
}

// ErrLayerNotFound is a layer whose owning row does not exist.
var ErrLayerNotFound = errors.New("policy layer not found")

// VersionStore reads a layer's history, newest first: below beforeVersion
// when it is not 0, at most pageSize versions.
type VersionStore interface {
	ListPolicyVersions(ctx context.Context, ref LayerRef, beforeVersion, pageSize int32) ([]Version, error)
}

// Page sizes for ListPolicyVersions.
const (
	DefaultVersionPageSize int32 = 50
	MaxVersionPageSize     int32 = 200
)

// Resource-name prefixes ListPolicyVersions addresses a layer by.
const (
	storageBackendsPrefix = "storageBackends/"
	tenantsPrefix         = "tenants/"
)

// VersionsPage is one page of a layer's history.
type VersionsPage struct {
	Versions []Version
	// NextPageToken resumes below the page's oldest version; "" on the last.
	NextPageToken string
}

// ListPolicyVersions pages through the texts the policy layer resourceName
// names has held: a storage backend, a bucket, a collection or a tenant. It
// is gated as GetEffectivePolicy is, since it returns policy text.
func (h *Handler) ListPolicyVersions(ctx context.Context, resourceName string, pageSize int32, pageToken string) (VersionsPage, error) {
	if h.versions == nil {
		return VersionsPage{}, connect.NewError(connect.CodeUnimplemented, "policy history is not wired")
	}
	ref, err := parseLayerName(resourceName)
	if err != nil {
		return VersionsPage{}, rpcerr.New(connect.CodeInvalidArgument, err)
	}
	before, err := parseVersionToken(pageToken)
	if err != nil {
		return VersionsPage{}, rpcerr.New(connect.CodeInvalidArgument, err)
	}
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return VersionsPage{}, connect.NewError(connect.CodeUnauthenticated, err.Error()).WithCause(err)
	}
	// A platform layer is checked against the caller's own tenant, as
	// GetEffectivePolicy checks a backend or bucket name.
	scope := ref.TenantID
	if scope == uuid.Nil {
		scope = p.TenantID
	}
	if err := h.authorizeInspect(ctx, scope, ref.Collection); err != nil {
		return VersionsPage{}, err
	}
	if ref.TenantID != uuid.Nil {
		// Read as the tenant asked about: its history is under its isolation.
		ctx = auth.WithActingTenant(ctx, ref.TenantID)
	}
	size := clampVersionPageSize(pageSize)
	// One more than the page, to learn whether another follows.
	versions, err := h.versions.ListPolicyVersions(ctx, ref, before, size+1)
	if errors.Is(err, ErrLayerNotFound) {
		return VersionsPage{}, connect.NewError(connect.CodeNotFound, err.Error()).WithCause(err)
	}
	if err != nil {
		return VersionsPage{}, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	out := VersionsPage{Versions: versions}
	if int32(len(versions)) > size {
		out.Versions = versions[:size]
		out.NextPageToken = strconv.FormatInt(int64(out.Versions[size-1].Version), 10)
	}
	return out, nil
}

func clampVersionPageSize(n int32) int32 {
	switch {
	case n <= 0:
		return DefaultVersionPageSize
	case n > MaxVersionPageSize:
		return MaxVersionPageSize
	}
	return n
}

// parseVersionToken reads a page token: the version a page resumes below,
// 0 for the first page.
func parseVersionToken(token string) (int32, error) {
	if token == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(token, 10, 32)
	if err != nil || n <= 0 {
		return 0, fmt.Errorf("page_token: %q is not a version", token)
	}
	return int32(n), nil
}

// parseLayerName reads storageBackends/{id}, storageBackends/{id}/buckets/{name},
// tenants/{id} or tenants/{id}/collections/{name}.
func parseLayerName(name string) (LayerRef, error) {
	if b, err := paladin.ParseBucketName(name); err == nil {
		id, err := uuid.Parse(b.Backend)
		if err != nil {
			return LayerRef{}, fmt.Errorf("invalid storage backend id in %q: %w", name, err)
		}
		return LayerRef{Layer: LayerBucket, BackendID: id, Bucket: b.Bucket}, nil
	}
	if c, err := paladin.ParseCollectionName(name); err == nil {
		id, err := uuid.Parse(c.Tenant)
		if err != nil {
			return LayerRef{}, fmt.Errorf("invalid tenant id in %q: %w", name, err)
		}
		return LayerRef{Layer: LayerCollection, TenantID: id, Collection: c.Collection}, nil
	}
	if rest, ok := strings.CutPrefix(name, storageBackendsPrefix); ok && !strings.Contains(rest, "/") {
		id, err := uuid.Parse(rest)
		if err != nil {
			return LayerRef{}, fmt.Errorf("invalid storage backend id in %q: %w", name, err)
		}
		return LayerRef{Layer: LayerStorageBackend, BackendID: id}, nil
	}
	if rest, ok := strings.CutPrefix(name, tenantsPrefix); ok && !strings.Contains(rest, "/") {
		id, err := uuid.Parse(rest)
		if err != nil {
			return LayerRef{}, fmt.Errorf("invalid tenant id in %q: %w", name, err)
		}
		return LayerRef{Layer: LayerTenant, TenantID: id}, nil
	}
	return LayerRef{}, fmt.Errorf("%q names no policy layer: want storageBackends/{id}, storageBackends/{id}/buckets/{name}, tenants/{id} or tenants/{id}/collections/{name}", name)
}
