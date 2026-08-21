//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// TestRotateCredentialsGraceWindow proves the dual-write rotation (migration
// 038): a grace>0 rotation preserves the prior ref + a now()+grace validity
// horizon; a grace=0 rotation swaps instantly and clears the window.
func TestRotateCredentialsGraceWindow(t *testing.T) {
	ctx := context.Background()
	pool := startPostgres(t)
	mustExec(t, ctx, pool,
		`INSERT INTO storage_backends (name, kind, credentials_secret_ref)
		 VALUES ('primary', 's3-compatible', 'ref-v1')`)
	repo := adapters.NewBackendRepoV2(sqlc.New(pool), pool)

	read := func(t *testing.T) (active, prev *string, validUntil *time.Time) {
		t.Helper()
		if err := pool.QueryRow(ctx,
			`SELECT credentials_secret_ref, previous_credentials_secret_ref,
			        previous_credentials_valid_until
			   FROM storage_backends WHERE name = 'primary'`,
		).Scan(&active, &prev, &validUntil); err != nil {
			t.Fatalf("read backend: %v", err)
		}
		return
	}
	deref := func(p *string) string {
		if p == nil {
			return "<nil>"
		}
		return *p
	}

	t.Run("grace>0 preserves prior ref + validity horizon", func(t *testing.T) {
		if err := repo.RotateCredentials(ctx, "primary", "ref-v2", 3600); err != nil {
			t.Fatalf("rotate: %v", err)
		}
		active, prev, validUntil := read(t)
		if deref(active) != "ref-v2" {
			t.Errorf("active = %s, want ref-v2", deref(active))
		}
		if deref(prev) != "ref-v1" {
			t.Errorf("previous = %s, want ref-v1", deref(prev))
		}
		if validUntil == nil {
			t.Fatal("previous_valid_until is NULL, want now()+1h")
		}
		// Within a wide band around now()+1h to absorb container clock skew.
		if d := time.Until(*validUntil); d < 50*time.Minute || d > 70*time.Minute {
			t.Errorf("valid_until in %v, want ~1h out", d)
		}
	})

	t.Run("grace=0 rotates instantly and clears the window", func(t *testing.T) {
		if err := repo.RotateCredentials(ctx, "primary", "ref-v3", 0); err != nil {
			t.Fatalf("rotate: %v", err)
		}
		active, prev, validUntil := read(t)
		if deref(active) != "ref-v3" {
			t.Errorf("active = %s, want ref-v3", deref(active))
		}
		if deref(prev) != "ref-v2" {
			t.Errorf("previous = %s, want ref-v2 (the just-superseded ref)", deref(prev))
		}
		if validUntil != nil {
			t.Errorf("valid_until = %v, want NULL for an instant rotation", *validUntil)
		}
	})
}
