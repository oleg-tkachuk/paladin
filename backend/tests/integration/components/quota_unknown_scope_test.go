//go:build integration

package components

import (
	"context"
	"errors"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// A quota for a tenant or a bucket that is not there used to reach the
// handler as the raw constraint violation — a foreign key for the tenant, the
// NOT NULL bucket_id the subselect left empty for the bucket — and was
// answered Internal. It is the caller naming something absent: ErrNotFound.
func TestQuotaUpsertForAnUnknownScopeIsNotFound(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	repo := adapters.NewQuotaRepoV2(sqlc.New(pool), pool)
	limit := new(int64(1 << 20))

	if err := repo.UpsertTenant(ctx, admindomain.Quota{TenantID: uuid.New(), MaxTotalBytes: limit}); !errors.Is(err, admindomain.ErrNotFound) {
		t.Errorf("unknown tenant: err = %v, want ErrNotFound", err)
	}
	if err := repo.UpsertBucket(ctx, admindomain.Quota{BackendID: "no-such-backend", BucketName: "no-such-bucket", MaxTotalBytes: limit}); !errors.Is(err, admindomain.ErrNotFound) {
		t.Errorf("unknown bucket: err = %v, want ErrNotFound", err)
	}
}
