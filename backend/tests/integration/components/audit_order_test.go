//go:build integration

package components

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/keyset"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// auditOrderOracle is each order_by as plain SQL orders it, written out here
// rather than read from the code under test.
var auditOrderOracle = map[string]string{
	admindomain.AuditOrderByAt:       "at",
	admindomain.AuditOrderByAction:   `action COLLATE "C"`,
	admindomain.AuditOrderByActor:    `actor_subject COLLATE "C"`,
	admindomain.AuditOrderByResource: `resource_name COLLATE "C"`,
	admindomain.AuditOrderByClient:   `COALESCE(user_agent, '') COLLATE "C"`,
}

// Walking the log sorted by any order_by, either way, pages by (value, id):
// every entry once, in the order SQL sorts them, under row-level security.
func TestListAudit_SortsByEveryOrderField(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)
	pool := rlsPool(t, ctx, admin)
	repo := adapters.NewAuditRepoV2(sqlc.New(pool), pool)
	tenant := uuid.New()
	mustExec(t, ctx, admin, `INSERT INTO tenants (id, slug, display_name) VALUES ($1, $2, $2)`, tenant, "t-"+tenant.String()[:8])
	asTenant := auth.WithActingTenant(ctx, tenant)

	// Values repeat, instants too, so the id tie-breaker carries the keyset.
	t0 := time.Date(2026, 10, 1, 0, 0, 0, 0, time.UTC)
	actions := []string{"iam.Login", "admin.CreateBucket", "Admin.Zeta", "iam.Login"}
	for g := range 23 {
		if err := repo.Insert(asTenant, admindomain.AuditEntry{
			At:            t0.Add(time.Duration(g%5) * time.Minute),
			ActorSubject:  []string{"bob", "alice", "Carol"}[g%3],
			ActorTenantID: tenant,
			ActorAudience: auth.AudienceAdmin,
			Action:        actions[g%len(actions)],
			ResourceName:  fmt.Sprintf("tenants/x/buckets/%d", g%4),
			// Empty is stored as NULL, which the order sorts as empty.
			UserAgent: []string{"paladin-sdk-go/0.97.0 gateway/1.4", "", "Paladin-sdk-python/0.97.0", ""}[g%4],
		}); err != nil {
			t.Fatal(err)
		}
	}
	if len(auditOrderOracle) != len(admindomain.AuditOrderFields) {
		t.Fatalf("the oracle names %d orders, the log accepts %d", len(auditOrderOracle), len(admindomain.AuditOrderFields))
	}

	for _, field := range admindomain.AuditOrderFields {
		for _, desc := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s desc=%v", field, desc), func(t *testing.T) {
				dir := "ASC"
				if desc {
					dir = "DESC"
				}
				want := idsOf(t, ctx, admin, fmt.Sprintf(
					`SELECT id FROM audit_log WHERE actor_tenant_id = $1 ORDER BY %s %s, id %s`,
					auditOrderOracle[field], dir, dir), tenant)

				args := admindomain.ListAuditArgs{
					ActorTenantID: tenant, PageSize: orderPageSize,
					Order: keyset.Order{Field: field, Desc: desc},
				}
				var got []uuid.UUID
				for pages := 0; ; pages++ {
					if pages > len(want) {
						t.Fatal("walk did not terminate")
					}
					page, next, err := repo.List(asTenant, args)
					if err != nil {
						t.Fatalf("page %d: %v", pages, err)
					}
					for _, e := range page {
						got = append(got, e.EntryID)
					}
					if next == "" {
						break
					}
					args.PageToken = next
				}
				if !slices.Equal(got, want) {
					t.Fatalf("walked\n%v\nwant\n%v", got, want)
				}
			})
		}
	}

	// A page token pages only the order it was minted for.
	_, next, err := repo.List(asTenant, admindomain.ListAuditArgs{
		ActorTenantID: tenant, PageSize: orderPageSize, Order: keyset.Order{Field: admindomain.AuditOrderByAction},
	})
	if err != nil || next == "" {
		t.Fatalf("first page: next %q, err %v", next, err)
	}
	_, _, err = repo.List(asTenant, admindomain.ListAuditArgs{
		ActorTenantID: tenant, PageSize: orderPageSize, PageToken: next,
		Order: keyset.Order{Field: admindomain.AuditOrderByActor},
	})
	if !errors.Is(err, keyset.ErrOrderChanged) {
		t.Fatalf("token reused under another order: err = %v, want ErrOrderChanged", err)
	}
}

// An order the function does not know is refused, not listed in some order.
func TestListAuditEntryIDs_RefusesAnUnknownOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)
	// Every predicate unset; only the order is under test.
	_, err := admin.Exec(ctx, `SELECT list_audit_entry_ids($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, false, $12, $13, 10)`,
		nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, "at; DROP TABLE audit_log", nil, nil)
	// The refusal is the function's own, not a call it could not resolve.
	if err == nil || !strings.Contains(err.Error(), "unknown order") {
		t.Fatalf("an unknown order: err = %v, want the function's refusal", err)
	}
}
