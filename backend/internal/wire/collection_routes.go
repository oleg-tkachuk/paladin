package wire

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/iam/v1/authh"
	objectkey "github.com/oleg-tkachuk/paladin/internal/api/v1/collection"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/tenant"
	"github.com/oleg-tkachuk/paladin/internal/config"
	policy "github.com/oleg-tkachuk/paladin/internal/policy/cedar"
)

const (
	// whoAmIRoutePageSize is the collection page pulled per List round-trip
	// while assembling a WhoAmI route-table page.
	whoAmIRoutePageSize = 200
	// whoAmIMaxRoutes caps the routes returned in ONE WhoAmI call. Beyond it the
	// caller pages via the returned next_page_token (ADR-0010 Phase 4). A
	// multiple of whoAmIRoutePageSize so the cap always lands on a ListCollections
	// page boundary — the boundary cursor is what we hand back for resumption.
	whoAmIMaxRoutes = 1000
)

// collectionLister is the slice of objectkey.Handler this needs — the
// authorized, tenant-scoped List. *objectkey.Handler satisfies it, so reusing
// the handler means the Cedar check + cross-tenant guard are exactly the ones
// the ListCollections RPC applies (one authz check per page, never per row).
type collectionLister interface {
	ListCollections(ctx context.Context, args objectkey.ListCollectionsArgs) ([]objectkey.Collection, string, error)
}

// defaultBindingReader is the slice of tenant.Repository this needs — reading
// the tenant's default route to decide which Collections get a bare (B) alias.
type defaultBindingReader interface {
	GetDefaultBinding(ctx context.Context, tenantID uuid.UUID) (tenant.DefaultBinding, error)
}

// collectionRouteLister assembles the WhoAmI Collection route table (ADR-0010
// Phase 4). Narrow interfaces (not the full handler / repository) keep it
// unit-testable with small fakes.
type collectionRouteLister struct {
	okH     collectionLister
	tenants defaultBindingReader
}

// ProvideCollectionRouteLister builds the WhoAmI route source. Wired into the
// auth handler via WithCollectionRoutes.
func ProvideCollectionRouteLister(repos Repos, pe *policy.Engine, cfg config.Config) authh.CollectionRouteLister {
	return collectionRouteLister{
		okH:     ProvideCollectionHandler(repos, pe, cfg),
		tenants: repos.Tenant,
	}
}

func (l collectionRouteLister) ListCollectionRoutes(ctx context.Context, tenantID uuid.UUID, pageToken string) ([]authh.CollectionRoute, string, error) {
	// The default binding decides which Collections expose a bare (B) alias: a
	// bare name resolves through the binding, so it round-trips to canonical
	// only for Collections in the default (backend, bucket). No binding → no
	// bare aliases (not an error).
	var dbBackend, dbBucket string
	if db, err := l.tenants.GetDefaultBinding(ctx, tenantID); err == nil {
		dbBackend, dbBucket = db.BackendID, db.BucketName
	} else if !errors.Is(err, tenant.ErrNotFound) {
		return nil, "", err
	}

	// Accumulate WHOLE ListCollections pages until the cap or exhaustion. Whole
	// pages keep the resume cursor on a page boundary (whoAmIMaxRoutes is a
	// multiple of whoAmIRoutePageSize), so next_page_token never skips a key.
	routes := make([]authh.CollectionRoute, 0, whoAmIRoutePageSize)
	cursor := pageToken
	for {
		keys, next, err := l.okH.ListCollections(ctx, objectkey.ListCollectionsArgs{
			TenantID:  tenantID,
			PageSize:  whoAmIRoutePageSize,
			PageToken: cursor,
		})
		if err != nil {
			return nil, "", err
		}
		for i := range keys {
			k := keys[i]
			bare := ""
			if dbBackend != "" && k.BackendID == dbBackend && k.BucketName == dbBucket {
				bare = k.Collection // B shape is the raw key; resolver completes it via the binding
			}
			routes = append(routes, authh.CollectionRoute{
				Canonical:  objectkey.CanonicalName(k.BackendID, k.BucketName, k.TenantID, k.Collection),
				TenantPath: objectkey.TenantPathName(k.TenantID, k.Collection),
				BareAlias:  bare,
				Backend:    k.BackendID,
				Bucket:     k.BucketName,
			})
		}
		cursor = next
		if next == "" {
			// Exhausted — this is the last page.
			return routes, "", nil
		}
		if len(routes) >= whoAmIMaxRoutes {
			// Cap reached with more keys remaining; hand back the boundary
			// cursor so the caller resumes exactly after the last route.
			return routes, cursor, nil
		}
	}
}
