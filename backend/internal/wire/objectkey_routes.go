package wire

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/iam/v1/authh"
	objectkey "github.com/oleg-tkachuk/paladin/internal/api/v1/object_key"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/tenant"
	"github.com/oleg-tkachuk/paladin/internal/config"
	policy "github.com/oleg-tkachuk/paladin/internal/policy/cedar"
)

const (
	// whoAmIRoutePageSize is the objectKey page pulled per List round-trip
	// while assembling the WhoAmI route table.
	whoAmIRoutePageSize = 200
	// whoAmIMaxRoutes caps the total routes WhoAmI returns. The route table
	// (ADR-0010 Phase 4) is a client-side normalization aid, not an inventory
	// API — a tenant with more ObjectKeys than this should page
	// ListObjectKeys directly. See BACKLOG for the pagination follow-up.
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

func (l objectKeyRouteLister) ListObjectKeyRoutes(ctx context.Context, tenantID uuid.UUID) ([]authh.ObjectKeyRoute, error) {
	// The default binding decides which ObjectKeys expose a bare (B) alias: a
	// bare name resolves through the binding, so it round-trips to canonical
	// only for ObjectKeys in the default (backend, bucket). No binding → no
	// bare aliases (not an error).
	var dbBackend, dbBucket string
	if db, err := l.tenants.GetDefaultBinding(ctx, tenantID); err == nil {
		dbBackend, dbBucket = db.BackendID, db.BucketName
	} else if !errors.Is(err, tenant.ErrNotFound) {
		return nil, err
	}

	var (
		routes    []authh.ObjectKeyRoute
		pageToken string
	)
	for len(routes) < whoAmIMaxRoutes {
		keys, next, err := l.okH.ListObjectKeys(ctx, objectkey.ListObjectKeysArgs{
			TenantID:  tenantID,
			PageSize:  whoAmIRoutePageSize,
			PageToken: pageToken,
		})
		if err != nil {
			return nil, err
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
			if len(routes) >= whoAmIMaxRoutes {
				break
			}
		}
		if next == "" {
			break
		}
		pageToken = next
	}
	return routes, nil
}
