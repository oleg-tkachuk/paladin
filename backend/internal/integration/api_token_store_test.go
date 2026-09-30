//go:build integration

// The api_token Postgres store had no test of any kind. That is how
// ListByTenant shipped with a SELECT of thirteen columns and a Scan of
// fourteen destinations: every call returned an error, so the console's
// API-token list was permanently broken, and the handler's own test used a
// fake store that could not disagree with the schema.
//
// These tests run the real store against the real schema. Where a method's
// correctness depends on RLS (tenant isolation), they use a NOBYPASSRLS pool
// so the policy actually engages — the suite's own pool is a superuser, for
// which RLS is inert.
package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/api_token"
	tokenstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/api_token/postgres"
)

// mkToken builds a Token with every field populated, so a round-trip that
// silently drops one fails rather than passing on zero == zero.
func mkToken(tenant uuid.UUID, name string, expires time.Time) api_token.Token {
	now := time.Now().UTC().Truncate(time.Microsecond)
	return api_token.Token{
		ID:           uuid.New(),
		TenantID:     tenant,
		Name:         name,
		Prefix:       "abcd1234",
		Scopes:       []string{"api:read", "api:write"},
		Roles:        []string{"tenant.admin"},
		Audience:     []string{"data", "admin"},
		ExpiresAt:    expires,
		RateLimitRPM: 120,
		CreatedBy:    "user:seed",
		CreatedAt:    now,
	}
}

func newTokenStore(t *testing.T, pool *pgxpool.Pool) *tokenstore.Store {
	t.Helper()
	s, err := tokenstore.New(pool)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}
	return s
}

// TestAPITokenRoundTrip pins that every column survives Insert → Get and
// Insert → FindByDigest, and that a miss is the sentinel rather than a
// wrapped pgx error the caller cannot branch on.
func TestAPITokenRoundTrip(t *testing.T) {
	ctx := context.Background()
	admin := startPostgres(t)
	tenant, _ := mkTenant(t, ctx, admin, "shared")
	store := newTokenStore(t, admin)

	want := mkToken(tenant, "ci-runner", time.Now().Add(24*time.Hour).UTC().Truncate(time.Microsecond))
	digest := []byte("digest-round-trip")
	if err := store.Insert(ctx, want, digest); err != nil {
		t.Fatalf("insert: %v", err)
	}

	for _, tc := range []struct {
		name string
		get  func() (api_token.Token, error)
	}{
		{"Get", func() (api_token.Token, error) { return store.Get(ctx, want.ID) }},
		{"FindByDigest", func() (api_token.Token, error) { return store.FindByDigest(ctx, digest) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.get()
			if err != nil {
				t.Fatalf("%s: %v", tc.name, err)
			}
			if got.ID != want.ID || got.TenantID != want.TenantID {
				t.Errorf("identity: got %v/%v want %v/%v", got.ID, got.TenantID, want.ID, want.TenantID)
			}
			if got.Name != want.Name || got.Prefix != want.Prefix || got.CreatedBy != want.CreatedBy {
				t.Errorf("strings: got %+v", got)
			}
			if got.RateLimitRPM != want.RateLimitRPM {
				t.Errorf("rate_limit_rpm: got %d want %d", got.RateLimitRPM, want.RateLimitRPM)
			}
			assertStrings(t, "scopes", got.Scopes, want.Scopes)
			assertStrings(t, "roles", got.Roles, want.Roles)
			assertStrings(t, "audience", got.Audience, want.Audience)
			if !got.ExpiresAt.Equal(want.ExpiresAt) {
				t.Errorf("expires_at: got %v want %v", got.ExpiresAt, want.ExpiresAt)
			}
			if got.RevokedAt != nil || got.LastUsedAt != nil {
				t.Errorf("fresh token should have nil revoked/last-used, got %v/%v", got.RevokedAt, got.LastUsedAt)
			}
		})
	}

	t.Run("missing digest is the sentinel", func(t *testing.T) {
		if _, err := store.FindByDigest(ctx, []byte("nope")); !errors.Is(err, api_token.ErrTokenNotFound) {
			t.Fatalf("want ErrTokenNotFound, got %v", err)
		}
	})
	t.Run("missing id is the sentinel", func(t *testing.T) {
		if _, err := store.Get(ctx, uuid.New()); !errors.Is(err, api_token.ErrTokenNotFound) {
			t.Fatalf("want ErrTokenNotFound, got %v", err)
		}
	})
}

// TestAPITokenListByTenant is the regression that the shipped scan-arity bug
// would have caught: with thirteen columns selected into fourteen
// destinations, every List call errored and the console showed no tokens over
// a populated table.
func TestAPITokenListByTenant(t *testing.T) {
	ctx := context.Background()
	admin := startPostgres(t)
	tenant, _ := mkTenant(t, ctx, admin, "shared")
	other, _ := mkTenant(t, ctx, admin, "shared")
	store := newTokenStore(t, admin)

	future := time.Now().Add(24 * time.Hour)
	past := time.Now().Add(-24 * time.Hour)

	active := mkToken(tenant, "active", future)
	revoked := mkToken(tenant, "revoked", future)
	expired := mkToken(tenant, "expired", past)
	foreign := mkToken(other, "foreign", future)

	for i, tok := range []api_token.Token{active, revoked, expired, foreign} {
		if err := store.Insert(ctx, tok, []byte{byte(i)}); err != nil {
			t.Fatalf("insert %s: %v", tok.Name, err)
		}
	}
	if err := store.Revoke(ctx, revoked.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}

	names := func(toks []api_token.Token) map[string]bool {
		m := make(map[string]bool, len(toks))
		for _, tk := range toks {
			m[tk.Name] = true
		}
		return m
	}

	t.Run("defaults hide revoked, expired, and other tenants", func(t *testing.T) {
		got, next, err := store.ListByTenant(ctx, api_token.ListByTenantArgs{TenantID: tenant})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if next != "" {
			t.Errorf("unexpected cursor %q", next)
		}
		if n := names(got); len(got) != 1 || !n["active"] {
			t.Fatalf("want only [active], got %v", n)
		}
	})

	t.Run("include_revoked widens", func(t *testing.T) {
		got, _, err := store.ListByTenant(ctx, api_token.ListByTenantArgs{TenantID: tenant, IncludeRevoked: true})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if n := names(got); !n["active"] || !n["revoked"] || n["expired"] || n["foreign"] {
			t.Fatalf("want [active revoked], got %v", n)
		}
	})

	t.Run("include_expired widens", func(t *testing.T) {
		got, _, err := store.ListByTenant(ctx, api_token.ListByTenantArgs{TenantID: tenant, IncludeExpired: true})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if n := names(got); !n["active"] || !n["expired"] || n["revoked"] || n["foreign"] {
			t.Fatalf("want [active expired], got %v", n)
		}
	})

	t.Run("cursor pages through without gaps or repeats", func(t *testing.T) {
		seen := map[string]int{}
		cursor := ""
		for page := 0; ; page++ {
			if page > 8 {
				t.Fatal("pagination did not terminate")
			}
			got, next, err := store.ListByTenant(ctx, api_token.ListByTenantArgs{
				TenantID: tenant, IncludeRevoked: true, IncludeExpired: true, Limit: 1, Cursor: cursor,
			})
			if err != nil {
				t.Fatalf("page %d: %v", page, err)
			}
			for _, tk := range got {
				seen[tk.Name]++
			}
			if next == "" {
				break
			}
			cursor = next
		}
		if len(seen) != 3 {
			t.Fatalf("want 3 distinct tokens across pages, got %v", seen)
		}
		for name, n := range seen {
			if n != 1 {
				t.Errorf("%s returned %d times, want exactly 1", name, n)
			}
		}
	})

	t.Run("tenant_id is required", func(t *testing.T) {
		if _, _, err := store.ListByTenant(ctx, api_token.ListByTenantArgs{}); err == nil {
			t.Fatal("want error for nil tenant_id")
		}
	})
}

// TestAPITokenRevokeAndTouch pins the two mutating methods that are not
// Insert: revocation is recorded and idempotent, and TouchLastUsed writes
// through the tenant GUC it sets for itself.
func TestAPITokenRevokeAndTouch(t *testing.T) {
	ctx := context.Background()
	admin := startPostgres(t)
	tenant, _ := mkTenant(t, ctx, admin, "shared")
	store := newTokenStore(t, admin)

	tok := mkToken(tenant, "revoke-me", time.Now().Add(time.Hour))
	if err := store.Insert(ctx, tok, []byte("d-revoke")); err != nil {
		t.Fatalf("insert: %v", err)
	}

	if err := store.Revoke(ctx, tok.ID); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	got, err := store.Get(ctx, tok.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.RevokedAt == nil {
		t.Fatal("revoked_at still nil after Revoke")
	}
	first := *got.RevokedAt

	// Second revoke must not move the timestamp: the WHERE revoked_at IS NULL
	// guard is what makes "when was this revoked" answerable.
	if err := store.Revoke(ctx, tok.ID); err != nil {
		t.Fatalf("second revoke: %v", err)
	}
	got, err = store.Get(ctx, tok.ID)
	if err != nil {
		t.Fatalf("get after second revoke: %v", err)
	}
	if !got.RevokedAt.Equal(first) {
		t.Errorf("revoked_at moved on re-revoke: %v → %v", first, *got.RevokedAt)
	}

	at := time.Now().UTC().Truncate(time.Microsecond)
	if err := store.TouchLastUsed(ctx, tok.ID, tenant, at); err != nil {
		t.Fatalf("touch: %v", err)
	}
	got, err = store.Get(ctx, tok.ID)
	if err != nil {
		t.Fatalf("get after touch: %v", err)
	}
	if got.LastUsedAt == nil || !got.LastUsedAt.Equal(at) {
		t.Errorf("last_used_at: got %v want %v", got.LastUsedAt, at)
	}
}

// TestAPITokenPurgeExpired pins the grace boundary. A purger that ignores the
// grace argument would delete tokens the operator can still see in the
// console, so the assertion is on both sides of the line.
func TestAPITokenPurgeExpired(t *testing.T) {
	ctx := context.Background()
	admin := startPostgres(t)
	tenant, _ := mkTenant(t, ctx, admin, "shared")
	store := newTokenStore(t, admin)

	longGone := mkToken(tenant, "long-gone", time.Now().Add(-48*time.Hour))
	justExpired := mkToken(tenant, "just-expired", time.Now().Add(-1*time.Minute))
	live := mkToken(tenant, "live", time.Now().Add(time.Hour))
	for i, tok := range []api_token.Token{longGone, justExpired, live} {
		if err := store.Insert(ctx, tok, []byte{0xA0, byte(i)}); err != nil {
			t.Fatalf("insert %s: %v", tok.Name, err)
		}
	}

	n, err := store.PurgeExpired(ctx, 24*time.Hour)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n != 1 {
		t.Fatalf("purged %d rows, want 1 (only the one expired beyond grace)", n)
	}
	if _, err := store.Get(ctx, longGone.ID); !errors.Is(err, api_token.ErrTokenNotFound) {
		t.Errorf("long-gone token survived purge: %v", err)
	}
	for _, keep := range []api_token.Token{justExpired, live} {
		if _, err := store.Get(ctx, keep.ID); err != nil {
			t.Errorf("%s was purged but should have survived: %v", keep.Name, err)
		}
	}
}

// TestAPITokenRLSIsolation runs the store on a NOBYPASSRLS pool, which is how
// the runtime connects. Insert sets the tenant GUC for itself; the read paths
// inherit whatever the request principal set, so a token belonging to another
// tenant must be invisible even when its id or digest is known exactly.
func TestAPITokenRLSIsolation(t *testing.T) {
	ctx := context.Background()
	admin := startPostgres(t)
	tenantA, _ := mkTenant(t, ctx, admin, "shared")
	tenantB, _ := mkTenant(t, ctx, admin, "shared")

	seed := newTokenStore(t, admin)
	tokB := mkToken(tenantB, "b-secret", time.Now().Add(time.Hour))
	digestB := []byte("digest-b")
	if err := seed.Insert(ctx, tokB, digestB); err != nil {
		t.Fatalf("seed insert: %v", err)
	}

	pool := rlsPool(t, ctx, admin)
	scoped := newTokenStore(t, pool)
	ctxA := auth.WithPrincipal(ctx, &auth.Principal{TenantID: tenantA})

	if _, err := scoped.Get(ctxA, tokB.ID); !errors.Is(err, api_token.ErrTokenNotFound) {
		t.Errorf("tenant A read B's token by id: %v", err)
	}
	if _, err := scoped.FindByDigest(ctxA, digestB); !errors.Is(err, api_token.ErrTokenNotFound) {
		t.Errorf("tenant A read B's token by digest: %v", err)
	}
	got, _, err := scoped.ListByTenant(ctxA, api_token.ListByTenantArgs{TenantID: tenantB})
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("tenant A listed %d of B's tokens", len(got))
	}
}

func assertStrings(t *testing.T, field string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Errorf("%s: got %v want %v", field, got, want)
		return
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("%s: got %v want %v", field, got, want)
			return
		}
	}
}
