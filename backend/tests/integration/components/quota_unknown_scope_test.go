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

	// The API sends the text to the caller: what is missing, with no
	// package names stacked in front of it.
	for _, tc := range []struct {
		scope string
		err   error
		want  string
	}{
		{"tenant", repo.UpsertTenant(ctx, admindomain.Quota{TenantID: uuid.New(), MaxTotalBytes: limit}), quotaNoTenant},
		{"bucket", repo.UpsertBucket(ctx, admindomain.Quota{BackendID: "no-such-backend", BucketName: "no-such-bucket", MaxTotalBytes: limit}), quotaNoBucket},
	} {
		if !errors.Is(tc.err, admindomain.ErrNotFound) {
			t.Errorf("unknown %s: err = %v, want ErrNotFound", tc.scope, tc.err)
		} else if tc.err.Error() != tc.want {
			t.Errorf("unknown %s: message %q, want %q", tc.scope, tc.err, tc.want)
		}
	}
}

// What a quota for a scope that is not there reads.
const (
	quotaNoTenant = "resource not found: no such tenant"
	quotaNoBucket = "resource not found: no such bucket"
)
