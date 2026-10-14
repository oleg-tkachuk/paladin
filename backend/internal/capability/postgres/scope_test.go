package postgres

import (
	"context"
	"reflect"
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

func (r *recordingTx) Exec(_ context.Context, _ string, args ...any) (pgconn.CommandTag, error) {
	r.set = append(r.set, setting{args[0].(string), args[1].(string)})
	return pgconn.CommandTag{}, nil
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
			[]setting{{settingTenant, tenant.String()}}},
		// A platform-wide read reads every tenant and writes none, as
		// paladin.cross_tenant widens only reads.
		{"platform-wide read", auth.WithCrossTenantRead(context.Background()),
			[]setting{{settingAllTenants, allTenantsRead}}},
		{"platform-wide read while acting on a tenant",
			auth.WithCrossTenantRead(auth.WithActingTenant(context.Background(), tenant)),
			[]setting{{settingAllTenants, allTenantsRead}}},
		// Housekeeping carries no principal; its pgstore scope decides.
		{"housekeeping", pgstore.WithAllTenants(context.Background()),
			[]setting{{settingAllTenants, allTenantsWrite}}},
		// The request's tenant wins over a pgstore scope it also carries.
		{"acting on a tenant with a pgstore scope", pgstore.WithTenant(auth.WithActingTenant(context.Background(), tenant), other),
			[]setting{{settingTenant, tenant.String()}}},
		{"no scope", context.Background(), nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tx := &recordingTx{}
			if err := Scope(tc.ctx, tx); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(tx.set, tc.want) {
				t.Errorf("settings = %v, want %v", tx.set, tc.want)
			}
		})
	}
}
