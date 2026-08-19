//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/auth/oauth"
)

// TestOAuthStore exercises the ADR-0009 OAuth AS store against real Postgres:
// client upsert/read roundtrip, code create + single-use consume, and the
// expiry + unknown-client failure paths.
func TestOAuthStore(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)
	store := oauth.NewPgxStore(pool)

	// Seed the FK rows: oauth_authorization_codes references users(user_id),
	// which references tenants(tenant_id).
	tenantID := uuid.New()
	userID := uuid.New()
	mustExec(t, ctx, pool, `INSERT INTO tenants (tenant_id, slug, display_name) VALUES ($1, 'acme', 'acme')`, tenantID)
	mustExec(t, ctx, pool,
		`INSERT INTO users (user_id, tenant_id, subject) VALUES ($1, $2, 'svc@acme')`, userID, tenantID)

	t.Run("client upsert + get roundtrip", func(t *testing.T) {
		want := oauth.Client{
			ClientID:         "claude-desktop",
			ClientName:       "Claude Desktop",
			RedirectURIs:     []string{"claude-desktop://callback", "https://claude.ai/cb"},
			AllowedScopes:    []string{"paladin.read", "paladin.write"},
			AllowedAudiences: []string{"paladin-data"},
			Public:           true,
		}
		if err := store.UpsertClient(ctx, want); err != nil {
			t.Fatalf("UpsertClient: %v", err)
		}
		got, err := store.GetClient(ctx, want.ClientID)
		if err != nil {
			t.Fatalf("GetClient: %v", err)
		}
		if got.ClientName != want.ClientName || len(got.RedirectURIs) != 2 ||
			len(got.AllowedScopes) != 2 || got.AllowedAudiences[0] != "paladin-data" || !got.Public {
			t.Fatalf("roundtrip mismatch: %+v", got)
		}
		// Upsert is idempotent — second call updates, doesn't error.
		want.ClientName = "Claude Desktop v2"
		if err := store.UpsertClient(ctx, want); err != nil {
			t.Fatalf("UpsertClient (update): %v", err)
		}
		got, _ = store.GetClient(ctx, want.ClientID)
		if got.ClientName != "Claude Desktop v2" {
			t.Fatalf("upsert did not update: %q", got.ClientName)
		}
	})

	t.Run("unknown client", func(t *testing.T) {
		if _, err := store.GetClient(ctx, "nope"); !errors.Is(err, oauth.ErrClientNotFound) {
			t.Fatalf("err = %v, want ErrClientNotFound", err)
		}
	})

	t.Run("code create + single-use consume", func(t *testing.T) {
		code, err := oauth.GenerateCode()
		if err != nil {
			t.Fatalf("GenerateCode: %v", err)
		}
		ac := oauth.AuthCode{
			Code:            code,
			ClientID:        "claude-desktop",
			UserID:          userID,
			TenantID:        tenantID,
			RedirectURI:     "claude-desktop://callback",
			CodeChallenge:   oauth.ComputeS256Challenge("verifier-xyz-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"),
			ChallengeMethod: oauth.PKCEMethodS256,
			Scopes:          []string{"paladin.read"},
			Audience:        "paladin-data",
			ExpiresAt:       time.Now().Add(60 * time.Second),
		}
		if err := store.CreateCode(ctx, ac); err != nil {
			t.Fatalf("CreateCode: %v", err)
		}

		got, err := store.ConsumeCode(ctx, code)
		if err != nil {
			t.Fatalf("ConsumeCode: %v", err)
		}
		if got.ClientID != ac.ClientID || got.UserID != userID || got.TenantID != tenantID ||
			got.Audience != "paladin-data" || got.RedirectURI != ac.RedirectURI || len(got.Scopes) != 1 {
			t.Fatalf("consumed code mismatch: %+v", got)
		}

		// Single-use: a second consume must fail.
		if _, err := store.ConsumeCode(ctx, code); !errors.Is(err, oauth.ErrCodeInvalid) {
			t.Fatalf("replay err = %v, want ErrCodeInvalid", err)
		}
	})

	t.Run("expired code rejected", func(t *testing.T) {
		code, _ := oauth.GenerateCode()
		ac := oauth.AuthCode{
			Code:            code,
			ClientID:        "claude-desktop",
			UserID:          userID,
			TenantID:        tenantID,
			RedirectURI:     "claude-desktop://callback",
			CodeChallenge:   "x",
			ChallengeMethod: oauth.PKCEMethodS256,
			Audience:        "paladin-data",
			ExpiresAt:       time.Now().Add(-time.Second), // already expired
		}
		if err := store.CreateCode(ctx, ac); err != nil {
			t.Fatalf("CreateCode: %v", err)
		}
		if _, err := store.ConsumeCode(ctx, code); !errors.Is(err, oauth.ErrCodeInvalid) {
			t.Fatalf("expired err = %v, want ErrCodeInvalid", err)
		}
	})

	t.Run("unknown code", func(t *testing.T) {
		if _, err := store.ConsumeCode(ctx, "does-not-exist"); !errors.Is(err, oauth.ErrCodeInvalid) {
			t.Fatalf("err = %v, want ErrCodeInvalid", err)
		}
	})
}
