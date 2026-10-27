//go:build integration

package components

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth/oauth"
)

// beforeSelfRegisteredMark is the last migration without
// oauth_clients.self_registered; 084 adds it and marks the clients already
// registered.
const beforeSelfRegisteredMark = 83

const (
	appRole    = "paladin_app"
	reaperRole = "paladin_reaper"
	liveFor    = time.Hour
	pastAge    = 48 * time.Hour
	maxAge     = 24 * time.Hour
	reapBatch  = 100
)

// clientFixture is a tenant with a user, the parents of a client's grants.
type clientFixture struct {
	pool             *pgxpool.Pool
	tenantID, userID uuid.UUID
}

func newClientFixture(t *testing.T, ctx context.Context, pool *pgxpool.Pool) clientFixture {
	t.Helper()
	f := clientFixture{pool: pool, tenantID: uuid.New(), userID: uuid.New()}
	slug := "t-" + f.tenantID.String()[:8]
	mustExec(t, ctx, pool, `INSERT INTO tenants (id, slug, display_name) VALUES ($1, $2, $2)`, f.tenantID, slug)
	mustExec(t, ctx, pool, `INSERT INTO users (id, tenant_id, subject) VALUES ($1, $2, $3)`,
		f.userID, f.tenantID, "agent@"+slug)
	return f
}

// grant stores a refresh token issued to clientID, live or not.
func (f clientFixture) grant(t *testing.T, ctx context.Context, clientID string, expiresIn time.Duration, revoked bool) uuid.UUID {
	t.Helper()
	id := uuid.New()
	mustExec(t, ctx, f.pool, `INSERT INTO refresh_tokens
		(id, user_id, tenant_id, expires_at, revoked, oauth_client_id, oauth_audience, oauth_scopes)
		VALUES ($1, $2, $3, now() + $4::interval, $5, $6, 'paladin-mcp', '{paladin.read}')`,
		id, f.userID, f.tenantID, expiresIn.String(), revoked, clientID)
	return id
}

// code stores an authorization code issued to clientID.
func (f clientFixture) code(t *testing.T, ctx context.Context, clientID string, expiresIn time.Duration, consumed bool) {
	t.Helper()
	mustExec(t, ctx, f.pool, `INSERT INTO oauth_authorization_codes
		(code_hash, oauth_client_id, user_id, tenant_id, redirect_uri, code_challenge,
		 code_challenge_method, scopes, audience, expires_at, consumed_at)
		SELECT $1, c.id, $2, $3, 'https://agent.invalid/cb', 'challenge', 'S256', '{paladin.read}',
		       'paladin-mcp', now() + $4::interval, CASE WHEN $5 THEN now() END
		FROM oauth_clients c WHERE c.client_id = $6`,
		[]byte(uuid.New().String()), f.userID, f.tenantID, expiresIn.String(), consumed, clientID)
}

// storeClient stores a client created age ago.
func storeClient(t *testing.T, ctx context.Context, pool *pgxpool.Pool, clientID string, selfRegistered bool, age time.Duration) {
	t.Helper()
	mustExec(t, ctx, pool, `INSERT INTO oauth_clients (client_id, self_registered, created_at)
		VALUES ($1, $2, now() - $3::interval)`, clientID, selfRegistered, age.String())
}

func clientRows(t *testing.T, ctx context.Context, pool *pgxpool.Pool, query string, args ...any) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(ctx, query, args...).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

// asRole runs fn on a transaction acting as role, scoped to tenant when one
// is given, and commits it.
func asRole(t *testing.T, ctx context.Context, pool *pgxpool.Pool, role string, tenant *uuid.UUID, fn func(tx pgx.Tx)) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	if _, err := tx.Exec(ctx, `SET LOCAL ROLE `+pgx.Identifier{role}.Sanitize()); err != nil {
		t.Fatalf("set role: %v", err)
	}
	if tenant != nil {
		if _, err := tx.Exec(ctx, `SELECT set_config('paladin.tenant_id', $1, true)`, tenant.String()); err != nil {
			t.Fatalf("set tenant: %v", err)
		}
	}
	fn(tx)
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit: %v", err)
	}
}

// Deleting a client, as the admin plane does on its app pool scoped to the
// admin's own tenant, takes its grants in every tenant and its codes with it.
func TestOAuthClientDeleteTakesItsGrants(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	admin := newClientFixture(t, ctx, pool)
	other := newClientFixture(t, ctx, pool)

	const agent, kept = "paladin-agent", "kept-agent"
	storeClient(t, ctx, pool, agent, true, 0)
	storeClient(t, ctx, pool, kept, true, 0)
	other.grant(t, ctx, agent, liveFor, false)
	other.code(t, ctx, agent, liveFor, false)
	keptGrant := other.grant(t, ctx, kept, liveFor, false)

	asRole(t, ctx, pool, appRole, &admin.tenantID, func(tx pgx.Tx) {
		if err := oauth.NewPgxStore(tx).DeleteClient(ctx, agent); err != nil {
			t.Fatalf("delete: %v", err)
		}
	})

	if n := clientRows(t, ctx, pool, `SELECT count(*) FROM oauth_clients WHERE client_id = $1`, agent); n != 0 {
		t.Error("the client survived its delete")
	}
	if n := clientRows(t, ctx, pool, `SELECT count(*) FROM refresh_tokens WHERE oauth_client_id = $1`, agent); n != 0 {
		t.Errorf("%d grant(s) in another tenant survived their client", n)
	}
	if n := clientRows(t, ctx, pool, `SELECT count(*) FROM oauth_authorization_codes ac
		WHERE NOT EXISTS (SELECT 1 FROM oauth_clients c WHERE c.id = ac.oauth_client_id)`); n != 0 {
		t.Errorf("%d code(s) survived their client", n)
	}
	if n := clientRows(t, ctx, pool, `SELECT count(*) FROM refresh_tokens WHERE id = $1`, keptGrant); n != 1 {
		t.Error("another client's grant went with it")
	}

	if err := oauth.NewPgxStore(pool).DeleteClient(ctx, agent); !errors.Is(err, oauth.ErrClientNotFound) {
		t.Errorf("deleting it again = %v, want ErrClientNotFound", err)
	}
}

// The reaper removes a self-registered client only when it is old enough and
// nothing it holds could still be used — and it sees grants in every tenant,
// as the reaper role does.
func TestOAuthClientReaperSparesWhatIsInUse(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	f := newClientFixture(t, ctx, pool)

	cases := []struct {
		name   string
		seed   func(clientID string)
		self   bool
		age    time.Duration
		reaped bool
	}{
		{name: "idle", self: true, age: pastAge, reaped: true},
		{name: "revoked-grant", self: true, age: pastAge, reaped: true,
			seed: func(id string) { f.grant(t, ctx, id, liveFor, true) }},
		{name: "expired-grant", self: true, age: pastAge, reaped: true,
			seed: func(id string) { f.grant(t, ctx, id, -liveFor, false) }},
		{name: "used-code", self: true, age: pastAge, reaped: true,
			seed: func(id string) { f.code(t, ctx, id, liveFor, true) }},
		{name: "live-grant", self: true, age: pastAge,
			seed: func(id string) { f.grant(t, ctx, id, liveFor, false) }},
		{name: "pending-code", self: true, age: pastAge,
			seed: func(id string) { f.code(t, ctx, id, liveFor, false) }},
		{name: "young", self: true, age: maxAge / 2},
		{name: "seeded", self: false, age: pastAge},
	}
	for _, c := range cases {
		storeClient(t, ctx, pool, c.name, c.self, c.age)
		if c.seed != nil {
			c.seed(c.name)
		}
	}

	var reaped int64
	asRole(t, ctx, pool, reaperRole, nil, func(tx pgx.Tx) {
		var err error
		now := time.Now()
		if reaped, err = oauth.NewPgxStore(tx).ReapIdleClients(ctx, now.Add(-maxAge), now, reapBatch); err != nil {
			t.Fatalf("reap: %v", err)
		}
	})

	want := 0
	for _, c := range cases {
		gone := clientRows(t, ctx, pool, `SELECT count(*) FROM oauth_clients WHERE client_id = $1`, c.name) == 0
		if gone != c.reaped {
			t.Errorf("%s: reaped = %v, want %v", c.name, gone, c.reaped)
		}
		if c.reaped {
			want++
		}
	}
	if reaped != int64(want) {
		t.Errorf("reaped %d, reported %d", want, reaped)
	}
}

// A run removes at most its batch, oldest first, and the next run the rest.
func TestOAuthClientReaperWorksInBatches(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	const batch = 2
	ids := []string{"oldest", "older", "old"}
	for i, id := range ids {
		storeClient(t, ctx, pool, id, true, pastAge+time.Duration(len(ids)-i)*time.Hour)
	}
	store := oauth.NewPgxStore(pool)
	now := time.Now()
	n, err := store.ReapIdleClients(ctx, now.Add(-maxAge), now, batch)
	if err != nil || n != batch {
		t.Fatalf("first run = %d, %v; want %d", n, err, batch)
	}
	if clientRows(t, ctx, pool, `SELECT count(*) FROM oauth_clients WHERE client_id = 'old'`) != 1 {
		t.Error("the batch did not take the oldest first")
	}
	if n, err = store.ReapIdleClients(ctx, now.Add(-maxAge), now, batch); err != nil || n != 1 {
		t.Errorf("second run = %d, %v; want 1", n, err)
	}
}

// The upsert keeps the mark it is given: a seeded client stays seeded, and
// one that registered itself stays self-registered.
func TestOAuthClientMarkRoundTrips(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	store := oauth.NewPgxStore(pool)
	for _, want := range []bool{true, false} {
		id := "client-" + uuid.New().String()
		if err := store.UpsertClient(ctx, oauth.Client{ClientID: id, SelfRegistered: want}); err != nil {
			t.Fatal(err)
		}
		got, err := store.GetClient(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if got.SelfRegistered != want {
			t.Errorf("self_registered = %v, want %v", got.SelfRegistered, want)
		}
	}
}

// Migrating marks the clients dynamic registration created before the
// column existed, and no seeded one.
func TestSelfRegisteredMarkIsBackfilled(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgresAt(t, beforeSelfRegisteredMark)
	db := stdlib.OpenDBFromPool(pool)
	t.Cleanup(func() { _ = db.Close() })

	registered := "paladin-" + uuid.NewV7().String()
	seeded := []string{"paladin-e2e-agent", "paladin-mcp-server",
		// A UUID that is not version 7 is not one registration made.
		"paladin-" + uuid.New().String()}
	for _, id := range append([]string{registered}, seeded...) {
		mustExec(t, ctx, pool, `INSERT INTO oauth_clients (client_id) VALUES ($1)`, id)
	}
	if err := goose.Up(db, "."); err != nil {
		t.Fatalf("migrate up: %v", err)
	}
	if clientRows(t, ctx, pool, `SELECT count(*) FROM oauth_clients WHERE client_id = $1 AND self_registered`, registered) != 1 {
		t.Error("a registered client was not marked")
	}
	for _, id := range seeded {
		if clientRows(t, ctx, pool, `SELECT count(*) FROM oauth_clients WHERE client_id = $1 AND self_registered`, id) != 0 {
			t.Errorf("%s was marked self-registered", id)
		}
	}
}
