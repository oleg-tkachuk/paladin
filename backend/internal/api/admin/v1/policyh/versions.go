package policyh

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"uuid"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"

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
	// Backend is the storage backend's name — its id in the admin API — for
	// LayerStorageBackend and LayerBucket.
	Backend string
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
	// WrittenByTenant is the writer's own tenant, where its subject names a
	// user; nil with WrittenBy, and for a version written before migration 102.
	WrittenByTenant *uuid.UUID
	WrittenAt       time.Time
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
	if err := h.authorizeLayer(ctx, ref); err != nil {
		return VersionsPage{}, err
	}
	if ref.TenantID != uuid.Nil() {
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
		h.log.Error("policy versions: read failed", zap.String("resource", resourceName), zap.Error(err))
		return VersionsPage{}, connect.NewError(connect.CodeInternal, err.Error()).WithCause(err)
	}
	return pageOf(versions, size), nil
}

// authorizeLayer gates reading ref's history as GetEffectivePolicy is gated:
// a platform layer (a backend or a bucket) against the caller's own tenant.
func (h *Handler) authorizeLayer(ctx context.Context, ref LayerRef) error {
	p, err := auth.PrincipalFromContext(ctx)
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, err.Error()).WithCause(err)
	}
	scope := ref.TenantID
	if scope == uuid.Nil() {
		scope = p.TenantID
	}
	return h.authorizeInspect(ctx, scope, ref.Collection)
}

// pageOf is the first size of versions, read one past the page: a version
// beyond it means another page follows, resuming below the page's oldest.
func pageOf(versions []Version, size int32) VersionsPage {
	if int32(len(versions)) <= size {
		return VersionsPage{Versions: versions}
	}
	page := versions[:size]
	return VersionsPage{Versions: page, NextPageToken: strconv.FormatInt(int64(page[size-1].Version), 10)}
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

// parseLayerName reads storageBackends/{backend}, storageBackends/{backend}/buckets/{name},
// tenants/{id} or tenants/{id}/collections/{name}. A backend is named as the
// admin API names it, by its name; a tenant by its id.
func parseLayerName(name string) (LayerRef, error) {
	if b, err := paladin.ParseBucketName(name); err == nil {
		return LayerRef{Layer: LayerBucket, Backend: b.Backend, Bucket: b.Bucket}, nil
	}
	if c, err := paladin.ParseCollectionName(name); err == nil {
		id, err := tenantIDIn(name, c.Tenant)
		if err != nil {
			return LayerRef{}, err
		}
		return LayerRef{Layer: LayerCollection, TenantID: id, Collection: c.Collection}, nil
	}
	if rest, ok := strings.CutPrefix(name, storageBackendsPrefix); ok && rest != "" && !strings.Contains(rest, "/") {
		return LayerRef{Layer: LayerStorageBackend, Backend: rest}, nil
	}
	if rest, ok := strings.CutPrefix(name, tenantsPrefix); ok && !strings.Contains(rest, "/") {
		id, err := tenantIDIn(name, rest)
		if err != nil {
			return LayerRef{}, err
		}
		return LayerRef{Layer: LayerTenant, TenantID: id}, nil
	}
	return LayerRef{}, fmt.Errorf("%q names no policy layer: want storageBackends/{backend}, storageBackends/{backend}/buckets/{name}, tenants/{id} or tenants/{id}/collections/{name}", name)
}
