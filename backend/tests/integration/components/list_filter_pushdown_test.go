//go:build integration

package components

import (
	"context"
	"fmt"
	"testing"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	objectkey "github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/collectionh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/tenanth"
	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// A filter used to select from the page rather than from the table: the repo
// read one page and the handler evaluated CEL over it, so a match past the
// page ceiling came back as an empty first page — which a client reasonably
// reads as "no such row". Each case here seeds more rows than fit in a page
// and asks for the one that sorts last, which only a SQL predicate can find.
//
// Every case also keeps the other half of the contract in view: the pushdown
// may only narrow, so a filter the walk cannot express must still return the
// page rather than an error or a wrong row.

func TestPushdown_Backends(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	repo := adapters.NewBackendRepoV2(sqlc.New(pool), pool)

	prefix := "pd-" + uuid.NewString()[:8]
	for i := range 5 {
		mustExec(t, ctx, pool,
			`INSERT INTO storage_backends (name, kind, provider, region, display_name, enabled)
			 VALUES ($1, 's3-compatible', $2, 'eu-central-1', $3, $4)`,
			fmt.Sprintf("%s-%02d", prefix, i), "minio",
			fmt.Sprintf("backend number %d", i), i%2 == 0)
	}
	last := fmt.Sprintf("%s-04", prefix)

	// Page size 2 — the match is on page three if the filter does not reach
	// the table.
	got, _, err := repo.List(ctx, 2, "", fmt.Sprintf(`backend_id == %q`, last))
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].BackendID != last {
		t.Fatalf("filter by name returned %d rows (%v), want just %s",
			len(got), names(got), last)
	}

	got, _, err = repo.List(ctx, 2, "", fmt.Sprintf(`backend_id.startsWith(%q) && enabled == false`, prefix))
	if err != nil {
		t.Fatalf("list enabled: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("prefix + bool returned %d rows (%v), want the 2 disabled ones",
			len(got), names(got))
	}
	for _, b := range got {
		if b.Enabled {
			t.Errorf("%s is enabled — the bool hint selected the wrong rows", b.BackendID)
		}
	}

	// A disjunction on one field is a set of values, and reaches the table
	// like a single one: both ends of the name order on one two-row page.
	first := fmt.Sprintf("%s-00", prefix)
	got, _, err = repo.List(ctx, 2, "", fmt.Sprintf(`backend_id == %q || backend_id == %q`, last, first))
	if err != nil {
		t.Fatalf("list disjunction: %v", err)
	}
	if len(got) != 2 || got[0].BackendID != first || got[1].BackendID != last {
		t.Fatalf("a one-field disjunction returned %v, want [%s %s]", names(got), first, last)
	}

	// A predicate the walk cannot express must not narrow anything: the repo
	// returns the page and the handler's CEL pass decides.
	got, _, err = repo.List(ctx, 100, "", `backend_id == "a" || provider == "b"`)
	if err != nil {
		t.Fatalf("list cross-field disjunction: %v", err)
	}
	if len(got) < 5 {
		t.Errorf("a disjunction over two fields narrowed the scan to %d rows — "+
			"pushdown must only narrow what it fully understands", len(got))
	}
}

func names(bs []admindomain.StorageBackend) []string {
	out := make([]string, 0, len(bs))
	for _, b := range bs {
		out = append(out, b.BackendID)
	}
	return out
}

func TestPushdown_Tenants(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	repo := adapters.NewTenantRepo(sqlc.New(pool), pool)

	prefix := "pd" + uuid.NewString()[:8]
	for i := range 5 {
		mustExec(t, ctx, pool,
			`INSERT INTO tenants (id, slug, display_name, storage_layout)
			 VALUES ($1, $2, $3, 'shared')`,
			uuid.New(), fmt.Sprintf("%s-%02d", prefix, i), fmt.Sprintf("tenant %d", i))
	}

	got, _, err := repo.List(ctx, tenanth.ListTenantsArgs{
		PageSize: 2,
		Filter:   fmt.Sprintf(`slug.startsWith(%q)`, prefix),
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	// Two of the five, because the page is two — but they must be two of
	// *these* five, not the two oldest tenants in the table.
	if len(got) != 2 {
		t.Fatalf("got %d rows, want a full page of 2", len(got))
	}
	for _, tn := range got {
		if len(tn.Slug) < len(prefix) || tn.Slug[:len(prefix)] != prefix {
			t.Errorf("slug %q does not match the filter — the page was filled "+
				"before the predicate ran", tn.Slug)
		}
	}

	// storage_layout is an enum column: a literal that is not a valid value
	// must return no rows, not fail the query.
	got, _, err = repo.List(ctx, tenanth.ListTenantsArgs{
		PageSize: 10,
		Filter:   `storage_layout == "not-a-layout"`,
	})
	if err != nil {
		t.Fatalf("an unknown enum literal broke the query: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d rows for an impossible layout", len(got))
	}
}

func TestPushdown_Operations(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	tenantID, _ := mkTenant(t, ctx, pool, "shared")
	repo := adapters.NewOperationRepo(sqlc.New(pool), pool)

	for i := range 5 {
		typ := "BatchCopy"
		if i == 4 {
			typ = "BatchUpdateTags"
		}
		mustExec(t, ctx, pool,
			`INSERT INTO operations (id, tenant_id, type, state) VALUES ($1, $2, $3, 'PENDING')`,
			uuid.New(), tenantID, typ)
	}

	// newestFirst=false: the assertion below reasons about UUIDv7 ordering
	// ("it sorts last"), which is the ascending page this test was written
	// against. The parameter arrived with the operations-widget fix
	// (c14798c3) and this call site was never recompiled — the suite is
	// build-tagged, so nothing noticed.
	got, _, err := repo.List(ctx, tenantID, nil, uuid.Nil, 2, `type == "BatchUpdateTags"`, false)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].Type != "BatchUpdateTags" {
		t.Fatalf("got %d rows, want the single BatchUpdateTags — operations are "+
			"keyed by UUIDv7 so it sorts last", len(got))
	}
}

// state and done are pushed too. Two finished operations bracket four running
// ones in id order, so a two-row page read without the predicate starts with
// the wrong kind of row whichever way the filter points.
func TestPushdown_OperationsStateAndDone(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	tenantID, _ := mkTenant(t, ctx, pool, "shared")
	repo := adapters.NewOperationRepo(sqlc.New(pool), pool)

	const pageSize = 2
	const running = 4
	finished := func() {
		mustExec(t, ctx, pool,
			`INSERT INTO operations (id, tenant_id, type, state, done_at) VALUES ($1, $2, 'BatchCopy', 'FAILED', now())`,
			uuid.Must(uuid.NewV7()), tenantID)
	}
	finished()
	for range running {
		mustExec(t, ctx, pool,
			`INSERT INTO operations (id, tenant_id, type, state) VALUES ($1, $2, 'BatchCopy', 'RUNNING')`,
			uuid.Must(uuid.NewV7()), tenantID)
	}
	finished()

	cases := []struct {
		filter string
		want   int
		keep   func(operationRow) bool
	}{
		{`state == "FAILED"`, 2, func(o operationRow) bool { return o.state == "FAILED" }},
		{`"RUNNING" == state`, pageSize, func(o operationRow) bool { return o.state == "RUNNING" }},
		{`done`, 2, func(o operationRow) bool { return o.done }},
		{`!done`, pageSize, func(o operationRow) bool { return !o.done }},
		// Not a value of the enum: no row, and no error from a failed cast.
		{`state == "NOT_A_STATE"`, 0, func(operationRow) bool { return false }},
		// A set of values, written as `in` or as a disjunction on the one
		// field, is pushed as one.
		{`state in ["FAILED", "CANCELLED"]`, 2, func(o operationRow) bool { return o.state == "FAILED" }},
		{`state == "CANCELLED" || state == "FAILED"`, 2, func(o operationRow) bool { return o.state == "FAILED" }},
		{`state in ["NOT_A_STATE", "FAILED"]`, 2, func(o operationRow) bool { return o.state == "FAILED" }},
	}
	for _, tc := range cases {
		t.Run(tc.filter, func(t *testing.T) {
			got, _, err := repo.List(ctx, tenantID, nil, uuid.Nil, pageSize, tc.filter, false)
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			if len(got) != tc.want {
				t.Fatalf("got %d rows, want %d", len(got), tc.want)
			}
			for _, o := range got {
				row := operationRow{state: string(o.State), done: o.DoneAt != nil}
				if !tc.keep(row) {
					t.Errorf("row %s (state %s, done %v) does not match %s — the predicate was not pushed",
						o.OperationID, row.state, row.done, tc.filter)
				}
			}
		})
	}
}

type operationRow struct {
	state string
	done  bool
}

func TestPushdown_Collections(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	tenantID, _ := mkTenant(t, ctx, pool, "shared")

	const backendID = "pd-coll-be"
	mustExec(t, ctx, pool,
		`INSERT INTO storage_backends (name, kind) VALUES ($1, 's3-compatible')
		 ON CONFLICT (name) DO NOTHING`, backendID)
	bucketID := uuid.New()
	mustExec(t, ctx, pool,
		`INSERT INTO buckets (id, backend_id, name, display_name)
		 SELECT $1, sb.id, 'pd-coll-bucket', 'pd' FROM storage_backends sb WHERE sb.name = $2`,
		bucketID, backendID)

	prefix := "pd" + uuid.NewString()[:8]
	for i := range 5 {
		mustExec(t, ctx, pool,
			`INSERT INTO collections (id, tenant_id, name, display_name, bucket_id)
			 VALUES ($1, $2, $3, $4, $5)`,
			uuid.New(), tenantID, fmt.Sprintf("%s-%02d", prefix, i),
			fmt.Sprintf("collection %d", i), bucketID)
	}
	last := fmt.Sprintf("%s-04", prefix)

	repo := adapters.NewCollectionRepo(sqlc.New(pool), pool)
	got, _, err := repo.List(ctx, objectkey.ListCollectionsArgs{
		TenantID: tenantID,
		PageSize: 2,
		Filter:   fmt.Sprintf(`collection == %q`, last),
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].Collection != last {
		t.Fatalf("got %d rows, want just %s", len(got), last)
	}
}

func TestPushdown_Users(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	tenantID, _ := mkTenant(t, ctx, pool, "shared")
	repo := adapters.NewUserRepo(sqlc.New(pool))

	prefix := "pd" + uuid.NewString()[:8]
	for i := range 5 {
		mustExec(t, ctx, pool,
			`INSERT INTO users (id, tenant_id, subject, display_name, password_hash, disabled)
			 VALUES ($1, $2, $3, $4, 'x', $5)`,
			uuid.New(), tenantID, fmt.Sprintf("%s-%02d@example.test", prefix, i),
			fmt.Sprintf("user %d", i), i == 4)
	}

	got, _, err := repo.List(ctx, authstore.ListUsersArgs{
		TenantID: tenantID,
		PageSize: 2,
		Filter:   fmt.Sprintf(`subject.startsWith(%q) && disabled`, prefix),
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d rows, want the single disabled user", len(got))
	}
}
