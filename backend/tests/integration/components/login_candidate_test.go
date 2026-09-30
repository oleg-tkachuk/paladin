//go:build integration

package components

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// The candidate set a Login authenticates against must not lose the real
// membership to leftovers.
//
// Login without a tenant hint checks the password against every row
// FindUsersBySubjectGlobal returns. The query caps at five, and the cap was
// written for a different job — deciding whether a subject is ambiguous —
// with no ORDER BY, so which five came back was the planner's choice.
//
// Then a fixture started leaving one membership per run behind, in a trashed
// tenant, all sharing the operator's subject. Measured on the compose stack:
// at six rows the real membership fell outside the cap and a correct password
// answered "invalid credentials". The suite locked itself out in about five
// runs, and the deployed cluster was one aborted run away from the same.
//
// Two properties, one test each.

// TestLoginCandidatesExcludeTrashedTenants is the fix that closes the case
// that actually happened: every leftover was in a trashed tenant, and nobody
// can sign in to a tenant in the trash.
func TestLoginCandidatesExcludeTrashedTenants(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)
	q := sqlc.New(pool)

	subject := "shared-" + uuid.NewString()[:8] + "@local"
	live, _ := mkTenant(t, ctx, pool, "shared")
	mustExec(t, ctx, pool,
		`INSERT INTO users (tenant_id, subject, password_hash, roles) VALUES ($1, $2, 'real', '{}')`,
		live, subject)

	// Six trashed memberships: one more than the cap, so without the
	// exclusion the live row is guaranteed to be crowded out.
	for i := 0; i < 6; i++ {
		gone, _ := mkTenant(t, ctx, pool, "shared")
		mustExec(t, ctx, pool,
			`INSERT INTO users (tenant_id, subject, password_hash, roles) VALUES ($1, $2, 'leftover', '{}')`,
			gone, subject)
		mustExec(t, ctx, pool, `UPDATE tenants SET deleted_at = now() WHERE id = $1`, gone)
	}

	got, err := q.FindUsersBySubjectGlobal(ctx, subject)
	if err != nil {
		t.Fatalf("FindUsersBySubjectGlobal: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("candidates = %d, want 1 — trashed memberships must not be "+
			"candidates, and there are six of them", len(got))
	}
	if got[0].TenantID.Bytes != [16]byte(live) {
		t.Errorf("the surviving candidate is not the live membership")
	}
}

// TestLoginCandidatesPreferRecentlyUsed pins the ordering. The cap can still
// truncate a subject live in more than five tenants, so which five it keeps
// has to be the ones being used — not whatever the planner returns.
func TestLoginCandidatesPreferRecentlyUsed(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)
	q := sqlc.New(pool)

	subject := "many-" + uuid.NewString()[:8] + "@local"
	var wanted uuid.UUID
	for i := 0; i < 7; i++ {
		tn, _ := mkTenant(t, ctx, pool, "shared")
		mustExec(t, ctx, pool,
			`INSERT INTO users (tenant_id, subject, password_hash, roles) VALUES ($1, $2, 'x', '{}')`,
			tn, subject)
		if i == 6 {
			// The last one created is the one signed into most recently —
			// creation order alone would put it last and drop it.
			wanted = tn
			mustExec(t, ctx, pool,
				`UPDATE users SET last_login_at = now() WHERE tenant_id = $1 AND subject = $2`,
				tn, subject)
		}
	}

	got, err := q.FindUsersBySubjectGlobal(ctx, subject)
	if err != nil {
		t.Fatalf("FindUsersBySubjectGlobal: %v", err)
	}
	if len(got) != 5 {
		t.Fatalf("candidates = %d, want the cap of 5", len(got))
	}
	if got[0].TenantID.Bytes != [16]byte(wanted) {
		t.Errorf("the most recently used membership is not first; the cap " +
			"would drop the tenant this subject actually signs in to")
	}
}

// TestNamedCrossTenantUserListNeedsTheActingScope pins the read that produced
// the leftovers in the first place.
//
// ListUsers with a NAMED parent passes the platform-admin gate and then ran
// with the RLS session still on the CALLER's tenant, so another tenant's users
// came back as an empty page — not an error, not a refusal, just a tenant that
// looked empty. The e2e teardown lists a tenant's users to delete them, so it
// deleted nothing, reported success, and left a user that blocked the purge.
//
// This asserts the property at the layer that decides it: the scope has to
// move to the tenant being listed, or the answer is silently wrong.
func TestNamedCrossTenantUserListNeedsTheActingScope(t *testing.T) {
	ctx := context.Background()
	admin := startPostgres(t)

	caller, _ := mkTenant(t, ctx, admin, "shared")
	target, _ := mkTenant(t, ctx, admin, "shared")
	mustExec(t, ctx, admin,
		`INSERT INTO users (tenant_id, subject, password_hash, roles)
		 VALUES ($1, 'someone@local', 'x', '{}')`, target)

	pool := rlsPool(t, ctx, admin)
	base := auth.WithPrincipal(ctx, &auth.Principal{TenantID: caller})

	count := func(c context.Context) int {
		t.Helper()
		var n int
		if err := pool.QueryRow(c, `SELECT count(*) FROM users`).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		return n
	}

	// The shape the handler used to have: authorized for the target, but
	// still scoped to the caller. The target's user is invisible.
	if got := count(base); got != 0 {
		t.Fatalf("caller's own scope sees %d users, want 0 — the fixture is wrong", got)
	}
	// The shape it has now.
	if got := count(auth.WithActingTenant(base, target)); got != 1 {
		t.Errorf("acting as the target sees %d users, want 1 — a teardown "+
			"reading this would delete nothing and call it success", got)
	}
}

// TestCreateBackendTwiceIsAlreadyExists is the runtime half: the adapter has
// to turn the unique violation into the sentinel, against a real Postgres.
//
// And the half that is easy to lose — TestBootstrapStillUpserts — proves the
// seeding path kept the behaviour it actually needs. Splitting one query into
// two is only correct if BOTH callers end up with the right one; a refactor
// that quietly gave bootstrap the plain INSERT would fail every restart after
// the first, and it would fail in a place nobody watches.
func TestCreateBackendTwiceIsAlreadyExists(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)
	repo := adapters.NewBackendRepoV2(sqlc.New(pool), pool)

	b := admindomain.StorageBackend{
		BackendID: "be-" + uuid.NewString()[:8],
		Kind:      "s3-compatible",
		Endpoint:  "http://x.invalid:3900",
		Region:    "us-east-1",
	}
	if err := repo.Create(ctx, b); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if err := repo.Create(ctx, b); !errors.Is(err, admindomain.ErrAlreadyExists) {
		t.Fatalf("second create = %v, want ErrAlreadyExists", err)
	}
}

func TestBootstrapStillUpserts(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)
	repo := adapters.NewBackendRepoV2(sqlc.New(pool), pool)

	b := admindomain.StorageBackend{
		BackendID: "be-" + uuid.NewString()[:8],
		Kind:      "s3-compatible",
		Endpoint:  "http://first.invalid:3900",
		Region:    "us-east-1",
	}
	if err := repo.Upsert(ctx, b); err != nil {
		t.Fatalf("first upsert: %v", err)
	}
	b.Endpoint = "http://second.invalid:3900"
	if err := repo.Upsert(ctx, b); err != nil {
		t.Fatalf("second upsert must converge, not refuse: %v", err)
	}
	got, err := repo.Get(ctx, b.BackendID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Endpoint != "http://second.invalid:3900" {
		t.Errorf("endpoint = %q; the seeding path must reconcile YAML onto the row", got.Endpoint)
	}
}
