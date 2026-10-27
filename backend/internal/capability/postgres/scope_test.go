package postgres

import (
	"context"
	"slices"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/oleg-tkachuk/limes/pgstore"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
)

// The settings the limes schema's policies read, as pgstore spells them, and
// the values limes.all_tenants takes.
const (
	settingTenant     = "limes.tenant_id"
	settingAllTenants = "limes.all_tenants"
	allTenantsWrite   = "on"
	allTenantsRead    = "read"
)

// setting is one set_config a scope made.
type setting struct{ name, value string }

// recordingTx records the settings a scope sets; any other Tx method panics
// through the nil embedded interface, which a scope must never call.
type recordingTx struct {
	pgx.Tx
	set []setting
}

// Exec records each name and value pair of a set_config statement.
func (r *recordingTx) Exec(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
	for i := 0; i+1 < len(args); i += 2 {
		r.set = append(r.set, setting{args[i].(string), args[i+1].(string)})
	}
	return pgconn.CommandTag{}, nil
}

// scoped is the settings of a scope: pgstore sets both every time, the one
// the scope does not use to nothing, so no default can widen it.
func scoped(tenant, all string) []setting {
	return []setting{{settingTenant, tenant}, {settingAllTenants, all}}
}

func TestScope(t *testing.T) {
	tenant := uuid.New()
	other := uuid.New()
	cases := []struct {
		name string
		ctx  context.Context
		want []setting
	}{
		{"acting on a tenant", auth.WithActingTenant(context.Background(), tenant),
			scoped(tenant.String(), "")},
		// A platform-wide read reads every tenant and writes none, as
		// paladin.cross_tenant widens only reads.
		{"platform-wide read", auth.WithCrossTenantRead(context.Background()),
			scoped("", allTenantsRead)},
		{"platform-wide read while acting on a tenant",
			auth.WithCrossTenantRead(auth.WithActingTenant(context.Background(), tenant)),
			scoped("", allTenantsRead)},
		// Housekeeping carries no principal; its pgstore scope decides.
		{"housekeeping", pgstore.WithAllTenants(context.Background()),
			scoped("", allTenantsWrite)},
		// The request's tenant wins over a pgstore scope it also carries.
		{"acting on a tenant with a pgstore scope", pgstore.WithTenant(auth.WithActingTenant(context.Background(), tenant), other),
			scoped(tenant.String(), "")},
		{"no scope", context.Background(), scoped("", "")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tx := &recordingTx{}
			if err := Scope(tc.ctx, tx); err != nil {
				t.Fatal(err)
			}
			if !slices.Equal(tx.set, tc.want) {
				t.Errorf("settings = %v, want %v", tx.set, tc.want)
			}
		})
	}
}
