//go:build integration

package components

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/operationh"

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

	prefix := "pd-" + uuid.New().String()[:8]
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

	// A disjunction across two fields runs one query per arm: walked a row a
	// page, it returns each match once and nothing else.
	filter := fmt.Sprintf(`backend_id.startsWith(%q) && (backend_id == %q || display_name == "backend number 1")`, prefix, last)
	seen := walkPages(t, func(token string) ([]admindomain.StorageBackend, string, error) {
		return repo.List(ctx, 1, token, filter)
	}, func(b admindomain.StorageBackend) string { return b.BackendID })
	if want := []string{prefix + "-01", last}; !slices.Equal(seen, want) {
		t.Errorf("cross-field disjunction walked %v, want %v", seen, want)
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

	prefix := "pd" + uuid.New().String()[:8]
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

// A label entry selects from the table: the labelled tenant is found on the
// first page however many unlabelled ones sort ahead of it, and tenants with
// the key at another value, or without it, are not fetched.
func TestPushdown_TenantLabels(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	repo := adapters.NewTenantRepo(sqlc.New(pool), pool)

	env := "pd" + uuid.New().String()[:8]
	for slug, labels := range map[string]string{
		env + "-none":  `{}`,
		env + "-dev":   `{"env": "dev"}`,
		env + "-other": `{"team": "core"}`,
	} {
		mustExec(t, ctx, pool,
			`INSERT INTO tenants (id, slug, display_name, storage_layout, labels)
			 VALUES ($1, $2, $2, 'shared', $3::jsonb)`, uuid.New(), slug, labels)
	}
	// The page runs in id order: the labelled tenant takes the highest id, so
	// a query that ignored the labels would fill the page with another.
	mustExec(t, ctx, pool,
		`INSERT INTO tenants (id, slug, display_name, storage_layout, labels)
		 VALUES (('ffffffff-ffff-4fff-bfff-' || substr(md5($1), 1, 12))::uuid, $1, $1, 'shared',
		         '{"env": "prod", "team": "core"}'::jsonb)`, env+"-prod")

	got, _, err := repo.List(ctx, tenanth.ListTenantsArgs{
		PageSize: 1,
		Filter:   fmt.Sprintf(`slug.startsWith(%q) && labels["env"] == "prod" && labels.team == "core"`, env),
	})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].Slug != env+"-prod" {
		var slugs []string
		for _, tn := range got {
			slugs = append(slugs, tn.Slug)
		}
		t.Errorf("page %v, want only %s-prod", slugs, env)
	}
}

// walkPages lists page after page from an empty token until the token runs
// out, and returns every row's name, in the order the pages gave them.
func walkPages[T any](t *testing.T, list func(token string) ([]T, string, error), name func(T) string) []string {
	t.Helper()
	const maxPages = 50
	var seen []string
	token := ""
	for range maxPages {
		page, next, err := list(token)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		for _, row := range page {
			seen = append(seen, name(row))
		}
		if next == "" {
			return seen
		}
		token = next
	}
	t.Fatalf("list did not end after %d pages", maxPages)
	return nil
}

// A disjunction across two fields selects from the table, one query per arm:
// walked one row per page, the list returns each tenant either arm matches,
// once, and none that neither does.
func TestPushdown_TenantsAcrossFields(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	repo := adapters.NewTenantRepo(sqlc.New(pool), pool)

	p := "pd" + uuid.New().String()[:8]
	for _, slug := range []string{"a", "b", "c", "d", "e"} {
		display := "other"
		if slug == "c" || slug == "e" {
			display = "pick"
		}
		mustExec(t, ctx, pool,
			`INSERT INTO tenants (id, slug, display_name, storage_layout) VALUES ($1, $2, $3, 'shared')`,
			uuid.New(), p+"-"+slug, p+"-"+display+"-"+slug)
	}
	filter := fmt.Sprintf(`slug.startsWith(%q) && (slug == "%s-a" || display_name.startsWith("%s-pick"))`, p, p, p)

	seen := walkPages(t, func(token string) ([]tenanth.Tenant, string, error) {
		args := tenanth.ListTenantsArgs{PageSize: 1, Filter: filter}
		if token != "" {
			id, err := uuid.Parse(token)
			if err != nil {
				return nil, "", err
			}
			args.AfterID = id
		}
		return repo.List(ctx, args)
	}, func(tn tenanth.Tenant) string { return tn.Slug })
	slices.Sort(seen)
	if want := []string{p + "-a", p + "-c", p + "-e"}; !slices.Equal(seen, want) {
		t.Errorf("walked %v, want %v", seen, want)
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
	got, _, err := repo.List(ctx, operationh.ListArgs{TenantID: tenantID, PageSize: 2, Filter: `type == "BatchUpdateTags"`})
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
			`INSERT INTO operations (id, tenant_id, type, state, done_at) VALUES ($1, $2, 'BatchCopy', 'FAILED', now())`, uuid.NewV7(), tenantID)
	}
	finished()
	for range running {
		mustExec(t, ctx, pool,
			`INSERT INTO operations (id, tenant_id, type, state) VALUES ($1, $2, 'BatchCopy', 'RUNNING')`, uuid.NewV7(), tenantID)
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
			got, _, err := repo.List(ctx, operationh.ListArgs{TenantID: tenantID, PageSize: pageSize, Filter: tc.filter})
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

// has_failures is pushed: a failed operation and a batch that left objects
// behind, never a clean one. Clean ones bracket them in id order, so a
// two-row page read without the predicate starts with the wrong kind.
func TestPushdown_OperationsWithFailures(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	tenantID, _ := mkTenant(t, ctx, pool, "shared")
	repo := adapters.NewOperationRepo(sqlc.New(pool), pool)

	const pageSize = 2
	const clean = 4
	insert := func(state string, failed int64) uuid.UUID {
		id := uuid.NewV7()
		mustExec(t, ctx, pool,
			`INSERT INTO operations (id, tenant_id, type, state, done_at, failed_count) VALUES ($1, $2, 'BatchDelete', $3, now(), $4)`,
			id, tenantID, state, failed)
		return id
	}
	for range clean / 2 {
		insert("SUCCEEDED", 0)
	}
	failed := insert("FAILED", 0)
	partial := insert("SUCCEEDED", 1)
	for range clean / 2 {
		insert("SUCCEEDED", 0)
	}

	got, _, err := repo.List(ctx, operationh.ListArgs{TenantID: tenantID, PageSize: pageSize, Filter: `has_failures`})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != pageSize || got[0].OperationID != failed || got[1].OperationID != partial {
		t.Fatalf("got %+v, want the failed operation and the partial batch — the predicate was not pushed", got)
	}
	if got[1].FailedCount != 1 {
		t.Errorf("partial batch's failed_count = %d, want 1", got[1].FailedCount)
	}
	clean2, _, err := repo.List(ctx, operationh.ListArgs{TenantID: tenantID, PageSize: clean, Filter: `!has_failures`})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	for _, o := range clean2 {
		if o.HasFailures() {
			t.Errorf("!has_failures listed %s (state %s, failed %d)", o.OperationID, o.State, o.FailedCount)
		}
	}
	if len(clean2) != clean {
		t.Errorf("!has_failures listed %d, want %d", len(clean2), clean)
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
		`INSERT INTO buckets (id, backend_id, name, display_name, owner_tenant_id)
		 SELECT $1, sb.id, 'pd-coll-bucket', 'pd', $3 FROM storage_backends sb WHERE sb.name = $2`,
		bucketID, backendID, tenantID)

	prefix := "pd" + uuid.New().String()[:8]
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

	// Across two fields: one query per arm, each match once.
	filter := fmt.Sprintf(`collection.startsWith(%q) && (collection == %q || display_name == "collection 1")`, prefix, last)
	seen := walkPages(t, func(token string) ([]objectkey.Collection, string, error) {
		return repo.List(ctx, objectkey.ListCollectionsArgs{TenantID: tenantID, PageSize: 1, PageToken: token, Filter: filter})
	}, func(c objectkey.Collection) string { return c.Collection })
	if want := []string{prefix + "-01", last}; !slices.Equal(seen, want) {
		t.Errorf("cross-field disjunction walked %v, want %v", seen, want)
	}
}

func TestPushdown_Users(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	tenantID, _ := mkTenant(t, ctx, pool, "shared")
	repo := adapters.NewUserRepo(sqlc.New(pool))

	prefix := "pd" + uuid.New().String()[:8]
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

	// Across two fields: one query per arm, each match once.
	filter := fmt.Sprintf(`subject.startsWith(%q) && (subject == "%s-04@example.test" || display_name == "user 1")`, prefix, prefix)
	seen := walkPages(t, func(token string) ([]authstore.User, string, error) {
		return repo.List(ctx, authstore.ListUsersArgs{TenantID: tenantID, PageSize: 1, PageToken: token, Filter: filter})
	}, func(u authstore.User) string { return u.Subject })
	slices.Sort(seen)
	if want := []string{prefix + "-01@example.test", prefix + "-04@example.test"}; !slices.Equal(seen, want) {
		t.Errorf("cross-field disjunction walked %v, want %v", seen, want)
	}
}

// Operations across two fields, oldest and newest first: one query per arm,
// merged in the order the page runs, each match once.
func TestPushdown_OperationsAcrossFields(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	tenantID, _ := mkTenant(t, ctx, pool, "shared")
	repo := adapters.NewOperationRepo(sqlc.New(pool), pool)

	var want []string
	for i, op := range []struct{ typ, state string }{
		{"BatchCopy", "PENDING"},
		{"BatchCopy", "RUNNING"},
		{"BatchCopy", "PENDING"},
		{"BatchUpdateTags", "PENDING"},
		{"BatchCopy", "PENDING"},
	} {
		id := uuid.New()
		mustExec(t, ctx, pool,
			`INSERT INTO operations (id, tenant_id, type, state) VALUES ($1, $2, $3, $4::operation_state)`,
			id, tenantID, op.typ, op.state)
		if i == 1 || i == 3 {
			want = append(want, id.String())
		}
	}
	// The list runs in id order; a uuid's canonical text sorts as its bytes.
	slices.Sort(want)
	filter := `type == "BatchUpdateTags" || state == "RUNNING"`
	for _, newestFirst := range []bool{false, true} {
		seen := walkPages(t, func(token string) ([]operationh.Operation, string, error) {
			args := operationh.ListArgs{TenantID: tenantID, PageSize: 1, Filter: filter, NewestFirst: newestFirst}
			if token != "" {
				id, err := uuid.Parse(token)
				if err != nil {
					return nil, "", err
				}
				args.AfterID = id
			}
			return repo.List(ctx, args)
		}, func(o operationh.Operation) string { return o.OperationID.String() })
		expect := slices.Clone(want)
		if newestFirst {
			slices.Reverse(expect)
		}
		if !slices.Equal(seen, expect) {
			t.Errorf("newest first %v: walked %v, want %v", newestFirst, seen, expect)
		}
	}
}

// Buckets across two fields: one query per arm, merged in (backend, name)
// order, each match once.
func TestPushdown_BucketsAcrossFields(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	owner, _ := mkTenant(t, ctx, pool, "shared")
	repo := adapters.NewBucketRepoV2(sqlc.New(pool), pool)

	backend := "pd-be-" + uuid.New().String()[:8]
	mustExec(t, ctx, pool, `INSERT INTO storage_backends (name, kind) VALUES ($1, 's3-compatible')`, backend)
	prefix := "pd" + uuid.New().String()[:8]
	for i := range 5 {
		display := "other"
		if i == 1 {
			display = "pick"
		}
		mustExec(t, ctx, pool,
			`INSERT INTO buckets (backend_id, name, display_name, owner_tenant_id)
			 SELECT id, $2, $3, $4 FROM storage_backends WHERE name = $1`,
			backend, fmt.Sprintf("%s-%02d", prefix, i), display, owner)
	}
	last := fmt.Sprintf("%s-04", prefix)
	filter := fmt.Sprintf(`bucket_id.startsWith(%q) && (bucket_id == %q || display_name == "pick")`, prefix, last)
	seen := walkPages(t, func(token string) ([]admindomain.Bucket, string, error) {
		args := admindomain.ListBucketsArgs{BackendID: backend, PageSize: 1, Filter: filter}
		if token != "" {
			args.AfterBackend, args.AfterName, _ = strings.Cut(token, "/")
		}
		return repo.List(ctx, args)
	}, func(b admindomain.Bucket) string { return b.BucketName })
	if want := []string{prefix + "-01", last}; !slices.Equal(seen, want) {
		t.Errorf("cross-field disjunction walked %v, want %v", seen, want)
	}
}
