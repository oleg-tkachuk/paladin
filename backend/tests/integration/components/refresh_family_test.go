//go:build integration

package components

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"

	authstore "github.com/oleg-tkachuk/paladin/backend/internal/auth/store"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// TestRefreshTokenFamilyRevoke verifies the ADR-0009 per-family reuse
// revocation against real Postgres (the schema baseline (001_initial_schema.sql)): RevokeFamilyOf revokes
// only the compromised chain, leaving other families intact.
func TestRefreshTokenFamilyRevoke(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)
	repo := adapters.NewRefreshTokenRepo(sqlc.New(pool))

	tenantID := uuid.New()
	userID := uuid.New()
	mustExec(t, ctx, pool, `INSERT INTO tenants (id, slug, display_name) VALUES ($1, 'acme', 'acme')`, tenantID)
	mustExec(t, ctx, pool, `INSERT INTO users (id, tenant_id, subject) VALUES ($1, $2, 'svc@acme')`, userID, tenantID)

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

// TestRefreshTokenUnusedSuccessor covers what lost-rotation recovery reads: a
// rotated token's parent, and whether it has been presented. A successor is
// offered until it is used, and never once its family is revoked.
func TestRefreshTokenUnusedSuccessor(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)
	repo := adapters.NewRefreshTokenRepo(sqlc.New(pool))

	tenantID := uuid.New()
	userID := uuid.New()
	mustExec(t, ctx, pool, `INSERT INTO tenants (id, slug, display_name) VALUES ($1, 'acme', 'acme')`, tenantID)
	mustExec(t, ctx, pool, `INSERT INTO users (id, tenant_id, subject) VALUES ($1, $2, 'svc@acme')`, userID, tenantID)

	family := uuid.New()
	mk := func(parent uuid.UUID) uuid.UUID {
		jti := uuid.Must(uuid.NewV7())
		if err := repo.Insert(ctx, authstore.RefreshToken{
			JTI: jti, FamilyID: family, UserID: userID, TenantID: tenantID,
			IssuedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour), ParentID: parent,
		}); err != nil {
			t.Fatalf("insert: %v", err)
		}
		return jti
	}
	login := mk(uuid.Nil)
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
