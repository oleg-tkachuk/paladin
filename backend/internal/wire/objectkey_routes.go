package wire

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin-private/internal/api/iam/v1/authh"
	objectkey "github.com/oleg-tkachuk/paladin-private/internal/api/v1/object_key"
	"github.com/oleg-tkachuk/paladin-private/internal/api/v1/tenant"
	"github.com/oleg-tkachuk/paladin-private/internal/config"
	policy "github.com/oleg-tkachuk/paladin-private/internal/policy/cedar"
)

const (
	// whoAmIRoutePageSize is the objectKey page pulled per List round-trip
	// while assembling a WhoAmI route-table page.
	whoAmIRoutePageSize = 200
	// whoAmIMaxRoutes caps the routes returned in ONE WhoAmI call. Beyond it the
	// caller pages via the returned next_page_token (ADR-0010 Phase 4). A
	// multiple of whoAmIRoutePageSize so the cap always lands on a ListObjectKeys
	// page boundary — the boundary cursor is what we hand back for resumption.
	whoAmIMaxRoutes = 1000
)

// objectKeyLister is the slice of objectkey.Handler this needs — the
// authorized, tenant-scoped List. *objectkey.Handler satisfies it, so reusing
// the handler means the Cedar check + cross-tenant guard are exactly the ones
// the ListObjectKeys RPC applies (one authz check per page, never per row).
type objectKeyLister interface {
	ListObjectKeys(ctx context.Context, args objectkey.ListObjectKeysArgs) ([]objectkey.ObjectKey, string, error)
}

// defaultBindingReader is the slice of tenant.Repository this needs — reading
// the tenant's default route to decide which ObjectKeys get a bare (B) alias.
type defaultBindingReader interface {
	GetDefaultBinding(ctx context.Context, tenantID uuid.UUID) (tenant.DefaultBinding, error)
}

// objectKeyRouteLister assembles the WhoAmI ObjectKey route table (ADR-0010
// Phase 4). Narrow interfaces (not the full handler / repository) keep it
// unit-testable with small fakes.
type objectKeyRouteLister struct {
	okH     objectKeyLister
	tenants defaultBindingReader
}

// ProvideObjectKeyRouteLister builds the WhoAmI route source. Wired into the
// auth handler via WithObjectKeyRoutes.
func ProvideObjectKeyRouteLister(repos Repos, pe *policy.Engine, cfg config.Config) authh.ObjectKeyRouteLister {
	return objectKeyRouteLister{
		okH:     ProvideObjectKeyHandler(repos, pe, cfg),
		tenants: repos.Tenant,
	}
}

func (l objectKeyRouteLister) ListObjectKeyRoutes(ctx context.Context, tenantID uuid.UUID, pageToken string) ([]authh.ObjectKeyRoute, string, error) {
	// The default binding decides which ObjectKeys expose a bare (B) alias: a
	// bare name resolves through the binding, so it round-trips to canonical
	// only for ObjectKeys in the default (backend, bucket). No binding → no
	// bare aliases (not an error).
	var dbBackend, dbBucket string
	if db, err := l.tenants.GetDefaultBinding(ctx, tenantID); err == nil {
		dbBackend, dbBucket = db.BackendID, db.BucketName
	} else if !errors.Is(err, tenant.ErrNotFound) {
		return nil, "", err
	}

	// Accumulate WHOLE ListObjectKeys pages until the cap or exhaustion. Whole
	// pages keep the resume cursor on a page boundary (whoAmIMaxRoutes is a
	// multiple of whoAmIRoutePageSize), so next_page_token never skips a key.
	routes := make([]authh.ObjectKeyRoute, 0, whoAmIRoutePageSize)
	cursor := pageToken
	for {
		keys, next, err := l.okH.ListObjectKeys(ctx, objectkey.ListObjectKeysArgs{
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
				bare = k.ObjectKey // B shape is the raw key; resolver completes it via the binding
			}
			routes = append(routes, authh.ObjectKeyRoute{
				Canonical:  objectkey.CanonicalName(k.BackendID, k.BucketName, k.TenantID, k.ObjectKey),
				TenantPath: objectkey.TenantPathName(k.TenantID, k.ObjectKey),
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
