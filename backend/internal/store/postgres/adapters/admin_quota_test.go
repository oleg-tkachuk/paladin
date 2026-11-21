package adapters

import (
	"errors"
	"fmt"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/schema"
)

// SQLSTATEs the quota upsert can raise.
const (
	sqlstateNotNull    = "23502"
	sqlstateForeignKey = "23503"
)

// A quota for a scope that is not there is ErrNotFound: the tenant by its
// foreign key, the bucket by the NOT NULL bucket_id its subquery left empty.
// Another constraint or column is not that, and passes through.
func TestMapQuotaOCCErrUnknownScope(t *testing.T) {
	for _, tc := range []struct {
		name     string
		in       error
		notFound bool
	}{
		{"unknown tenant", fmt.Errorf("upsert: %w", &pgconn.PgError{Code: sqlstateForeignKey, ConstraintName: schema.QuotasTenantIDFK}), true},
		{"unknown bucket", &pgconn.PgError{Code: sqlstateNotNull, ColumnName: schema.BucketQuotasBucketIDColumn}, true},
		{"another foreign key", &pgconn.PgError{Code: sqlstateForeignKey, ConstraintName: "quotas_other_fkey"}, false},
		{"another column", &pgconn.PgError{Code: sqlstateNotNull, ColumnName: "max_total_bytes"}, false},
		{"not null under the tenant's constraint name", &pgconn.PgError{Code: sqlstateNotNull, ConstraintName: schema.QuotasTenantIDFK}, false},
		{"foreign key on the bucket column", &pgconn.PgError{Code: sqlstateForeignKey, ColumnName: schema.BucketQuotasBucketIDColumn}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := mapQuotaOCCErr(tc.in)
			if errors.Is(got, admindomain.ErrNotFound) != tc.notFound {
				t.Fatalf("got %v, want not found = %v", got, tc.notFound)
			}
			if !tc.notFound && !errors.Is(got, tc.in) {
				t.Fatalf("got %v, want %v passed through", got, tc.in)
			}
		})
	}
}
