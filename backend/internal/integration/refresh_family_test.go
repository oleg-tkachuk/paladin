//go:build integration

package integration

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	authstore "github.com/oleg-tkachuk/paladin-private/internal/auth/store"
	"github.com/oleg-tkachuk/paladin-private/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin-private/internal/store/postgres/sqlc"
)

// TestRefreshTokenFamilyRevoke verifies the ADR-0009 per-family reuse
// revocation against real Postgres (migration 044): RevokeFamilyOf revokes
// only the compromised chain, leaving other families intact.
func TestRefreshTokenFamilyRevoke(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)
	repo := adapters.NewRefreshTokenRepo(sqlc.New(pool))

	tenantID := uuid.New()
	userID := uuid.New()
	mustExec(t, ctx, pool, `INSERT INTO tenants (tenant_id, slug, display_name) VALUES ($1, 'acme', 'acme')`, tenantID)
	mustExec(t, ctx, pool, `INSERT INTO users (user_id, tenant_id, subject) VALUES ($1, $2, 'svc@acme')`, userID, tenantID)

	famA, famB := uuid.New(), uuid.New()
	mk := func(family uuid.UUID) uuid.UUID {
		jti := uuid.Must(uuid.NewV7())
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
