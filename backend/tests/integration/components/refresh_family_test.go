//go:build integration

package components

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// TestRefreshTokenFamilyRevoke verifies the ADR-0009 per-family reuse
// revocation against real Postgres (the schema baseline (001_initial_schema.sql)): RevokeFamilyOf revokes
// only the compromised chain, leaving other families intact.
func TestRefreshTokenFamilyRevoke(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	repo := adapters.NewRefreshTokenRepo(sqlc.New(pool))

	tenantID := uuid.New()
	userID := uuid.New()
	mustExec(t, ctx, pool, `INSERT INTO tenants (id, slug, display_name) VALUES ($1, 'acme', 'acme')`, tenantID)
	mustExec(t, ctx, pool, `INSERT INTO users (id, tenant_id, subject) VALUES ($1, $2, 'svc@acme')`, userID, tenantID)

	famA, famB := uuid.New(), uuid.New()
	mk := func(family uuid.UUID) uuid.UUID {
		jti := uuid.NewV7()
		if err := repo.Insert(ctx, authstore.RefreshToken{
			JTI: jti, FamilyID: family, UserID: userID, TenantID: tenantID,
			IssuedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
		}); err != nil {
			t.Fatalf("insert: %v", err)
		}
		return jti
	}
	a1, a2, b1 := mk(famA), mk(famA), mk(famB)

	// Revoke family A via one of its tokens.
	n, err := repo.RevokeFamilyOf(ctx, a1)
	if err != nil {
		t.Fatalf("RevokeFamilyOf: %v", err)
	}
	if n != 2 {
		t.Fatalf("revoked %d tokens, want 2 (the whole family A)", n)
	}

	// Both family-A tokens are now revoked...
	if _, err := repo.Get(ctx, a2); !errors.Is(err, authstore.ErrTokenRevoked) {
		t.Errorf("a2 err = %v, want ErrTokenRevoked", err)
	}
	// ...while family B is untouched.
	if got, err := repo.Get(ctx, b1); err != nil || got.FamilyID != famB {
		t.Errorf("b1 should remain valid in family B: err=%v fam=%v", err, got.FamilyID)
	}
}

// TestRefreshTokenUnusedSuccessor covers what lost-rotation recovery reads: a
// rotated token's parent, and whether it has been presented. A successor is
// offered until it is used, and never once its family is revoked.
func TestRefreshTokenUnusedSuccessor(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	repo := adapters.NewRefreshTokenRepo(sqlc.New(pool))

	tenantID := uuid.New()
	userID := uuid.New()
	mustExec(t, ctx, pool, `INSERT INTO tenants (id, slug, display_name) VALUES ($1, 'acme', 'acme')`, tenantID)
	mustExec(t, ctx, pool, `INSERT INTO users (id, tenant_id, subject) VALUES ($1, $2, 'svc@acme')`, userID, tenantID)

	family := uuid.New()
	mk := func(parent uuid.UUID) uuid.UUID {
		jti := uuid.NewV7()
		if err := repo.Insert(ctx, authstore.RefreshToken{
			JTI: jti, FamilyID: family, UserID: userID, TenantID: tenantID,
			IssuedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour), ParentID: parent,
		}); err != nil {
			t.Fatalf("insert: %v", err)
		}
		return jti
	}
	login := mk(uuid.Nil())
	succ := mk(login)

	if got, err := repo.GetAny(ctx, succ); err != nil || got.ParentID != login {
		t.Fatalf("parent not stored: err=%v parent=%v", err, got.ParentID)
	}
	if got, err := repo.UnusedSuccessor(ctx, login); err != nil || got.JTI != succ {
		t.Fatalf("unused successor = %v, %v; want %v", got.JTI, err, succ)
	}
	if _, err := repo.UnusedSuccessor(ctx, succ); !errors.Is(err, authstore.ErrNotFound) {
		t.Fatalf("a token with no successor: err = %v, want ErrNotFound", err)
	}

	if err := repo.MarkUsed(ctx, succ); err != nil {
		t.Fatalf("MarkUsed: %v", err)
	}
	first, _ := repo.GetAny(ctx, succ)
	if first.FirstUsedAt == nil {
		t.Fatal("first_used_at not recorded")
	}
	if _, err := repo.UnusedSuccessor(ctx, login); !errors.Is(err, authstore.ErrNotFound) {
		t.Fatalf("a used successor was offered: err = %v", err)
	}
	// Later presentations leave the first one standing.
	if err := repo.MarkUsed(ctx, succ); err != nil {
		t.Fatalf("MarkUsed again: %v", err)
	}
	if again, _ := repo.GetAny(ctx, succ); !again.FirstUsedAt.Equal(*first.FirstUsedAt) {
		t.Errorf("first_used_at moved from %v to %v", first.FirstUsedAt, again.FirstUsedAt)
	}

	unused := mk(succ)
	if _, err := repo.RevokeFamilyOf(ctx, login); err != nil {
		t.Fatalf("RevokeFamilyOf: %v", err)
	}
	if _, err := repo.UnusedSuccessor(ctx, succ); !errors.Is(err, authstore.ErrNotFound) {
		t.Fatalf("a revoked family's successor %v was offered: err = %v", unused, err)
	}
}

// TestRefreshTokenOAuthGrant covers ADR-0035 against real Postgres: a token
// issued under an OAuth grant reads back with its client, audience and scopes,
// including a consent to no scope at all; a console login reads back with no
// grant; and the table refuses a grant recorded in part.
func TestRefreshTokenOAuthGrant(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	repo := adapters.NewRefreshTokenRepo(sqlc.New(pool))

	const clientID = "desktop-agent"
	tenantID := uuid.New()
	userID := uuid.New()
	mustExec(t, ctx, pool, `INSERT INTO tenants (id, slug, display_name) VALUES ($1, 'acme', 'acme')`, tenantID)
	mustExec(t, ctx, pool, `INSERT INTO users (id, tenant_id, subject) VALUES ($1, $2, 'svc@acme')`, userID, tenantID)
	mustExec(t, ctx, pool, `INSERT INTO oauth_clients (client_id) VALUES ($1)`, clientID)

	insert := func(grant *authstore.OAuthGrant) uuid.UUID {
		t.Helper()
		jti := uuid.NewV7()
		if err := repo.Insert(ctx, authstore.RefreshToken{
			JTI: jti, FamilyID: uuid.New(), UserID: userID, TenantID: tenantID,
			IssuedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour), OAuth: grant,
		}); err != nil {
			t.Fatalf("insert: %v", err)
		}
		return jti
	}

	for _, want := range []authstore.OAuthGrant{
		{ClientID: clientID, Audience: "paladin-admin", Scopes: []string{"paladin.read", "paladin.write"}},
		// No scope consented: the handler holds a nil slice, not an empty one.
		{ClientID: clientID, Audience: "paladin-data", Scopes: nil},
	} {
		got, err := repo.Get(ctx, insert(&want))
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.OAuth == nil || got.OAuth.ClientID != want.ClientID || got.OAuth.Audience != want.Audience ||
			len(got.OAuth.Scopes) != len(want.Scopes) {
			t.Errorf("grant = %+v, want %+v", got.OAuth, want)
		}
	}

	console, err := repo.Get(ctx, insert(nil))
	if err != nil {
		t.Fatalf("get console token: %v", err)
	}
	if console.OAuth != nil {
		t.Errorf("a console login read back with a grant: %+v", console.OAuth)
	}

	// A client without the audience and scopes it was granted is not a grant.
	if _, err := pool.Exec(ctx, `INSERT INTO refresh_tokens (id, user_id, tenant_id, expires_at, oauth_client_id)
		VALUES ($1, $2, $3, now() + interval '1 hour', $4)`, uuid.New(), userID, tenantID, clientID); err == nil {
		t.Error("a half-recorded grant was stored")
	}
}

// The grants a user holds are its live OAuth families, one row each, and
// ending one ends that family alone — never a console session, never another
// user's grant.
func TestOAuthGrantsListAndRevoke(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	repo := adapters.NewRefreshTokenRepo(sqlc.New(pool))

	const clientID = "desktop-agent"
	tenantID := uuid.New()
	alice, bob := uuid.New(), uuid.New()
	mustExec(t, ctx, pool, `INSERT INTO tenants (id, slug, display_name) VALUES ($1, 'acme', 'acme')`, tenantID)
	mustExec(t, ctx, pool, `INSERT INTO users (id, tenant_id, subject) VALUES ($1, $2, 'alice@acme')`, alice, tenantID)
	mustExec(t, ctx, pool, `INSERT INTO users (id, tenant_id, subject) VALUES ($1, $2, 'bob@acme')`, bob, tenantID)
	mustExec(t, ctx, pool, `INSERT INTO oauth_clients (client_id) VALUES ($1)`, clientID)

	grant := &authstore.OAuthGrant{ClientID: clientID, Audience: "paladin-mcp", Scopes: []string{"paladin.read"}}
	now := time.Now().UTC().Truncate(time.Microsecond)
	insert := func(user, family uuid.UUID, g *authstore.OAuthGrant, issued time.Time, ttl time.Duration) uuid.UUID {
		t.Helper()
		jti := uuid.NewV7()
		if err := repo.Insert(ctx, authstore.RefreshToken{
			JTI: jti, FamilyID: family, UserID: user, TenantID: tenantID,
			IssuedAt: issued, ExpiresAt: issued.Add(ttl), OAuth: g,
		}); err != nil {
			t.Fatalf("insert: %v", err)
		}
		return jti
	}

	// Alice's grant, rotated once: the first token superseded, the second live.
	rotated := uuid.New()
	first := insert(alice, rotated, grant, now.Add(-2*time.Hour), 24*time.Hour)
	if err := repo.Supersede(ctx, first); err != nil {
		t.Fatal(err)
	}
	insert(alice, rotated, grant, now.Add(-time.Hour), 24*time.Hour)
	// Another live grant, a console session, an expired grant, and Bob's.
	other := uuid.New()
	insert(alice, other, grant, now, 24*time.Hour)
	console := uuid.New()
	insert(alice, console, nil, now, 24*time.Hour)
	expired := uuid.New()
	insert(alice, expired, grant, now.Add(-48*time.Hour), time.Hour)
	bobs := uuid.New()
	insert(bob, bobs, grant, now, 24*time.Hour)

	got, err := repo.ListOAuthGrants(ctx, alice, now, false)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("grants = %d, want 2 (the rotated one once, and the other)", len(got))
	}
	byID := map[uuid.UUID]authstore.UserOAuthGrant{}
	for _, g := range got {
		byID[g.ID] = g
	}
	r := byID[rotated]
	if !r.GrantedAt.Equal(now.Add(-2*time.Hour)) || !r.RefreshedAt.Equal(now.Add(-time.Hour)) {
		t.Errorf("rotated grant: granted %s, refreshed %s; want the first and the newest token's times", r.GrantedAt, r.RefreshedAt)
	}
	if r.Grant.ClientID != clientID || r.Grant.Audience != grant.Audience || len(r.Grant.Scopes) != 1 {
		t.Errorf("rotated grant = %+v", r.Grant)
	}

	for _, tc := range []struct {
		name  string
		user  uuid.UUID
		grant uuid.UUID
	}{
		{"another user's grant", alice, bobs},
		{"a console session", alice, console},
		{"a grant asked for as another user", bob, rotated},
	} {
		if found, err := repo.RevokeOAuthGrant(ctx, tc.user, tc.grant); err != nil || found {
			t.Errorf("%s: revoked = %v (%v), want untouched", tc.name, found, err)
		}
	}
	if found, err := repo.RevokeOAuthGrant(ctx, alice, rotated); err != nil || !found {
		t.Fatalf("revoke own grant: found = %v, err = %v", found, err)
	}
	if got, _ := repo.ListOAuthGrants(ctx, alice, now, false); len(got) != 1 || got[0].ID != other {
		t.Errorf("after revoking, grants = %+v, want only %s", got, other)
	}
	// Asked for the inactive ones too, every grant whose rows remain shows,
	// each in the state its newest token gives it; the console session does not.
	all, err := repo.ListOAuthGrants(ctx, alice, now, true)
	if err != nil {
		t.Fatal(err)
	}
	states := map[uuid.UUID]authstore.OAuthGrantState{}
	for _, g := range all {
		states[g.ID] = g.State
	}
	want := map[uuid.UUID]authstore.OAuthGrantState{
		rotated: authstore.OAuthGrantRevoked,
		other:   authstore.OAuthGrantActive,
		expired: authstore.OAuthGrantExpired,
	}
	if len(states) != len(want) {
		t.Errorf("with inactive, grants = %v, want %v", states, want)
	}
	for id, w := range want {
		if states[id] != w {
			t.Errorf("grant %s: state = %v, want %v", id, states[id], w)
		}
	}
	if got, _ := repo.ListOAuthGrants(ctx, bob, now, false); len(got) != 1 {
		t.Errorf("bob's grants = %d after alice revoked hers, want 1", len(got))
	}
}
