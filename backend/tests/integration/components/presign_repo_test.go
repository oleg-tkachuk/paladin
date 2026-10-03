//go:build integration

package components

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/presignh"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

func presignDeadline(t *testing.T, ctx context.Context, pool *pgxpool.Pool, id uuid.UUID) *time.Time {
	t.Helper()
	var at *time.Time
	if err := pool.QueryRow(ctx, `SELECT presign_expires_at FROM objects WHERE id = $1`, id).Scan(&at); err != nil {
		t.Fatalf("read presign_expires_at: %v", err)
	}
	return at
}

// RegenerateUploadUrl left presign_expires_at at the FIRST URL's expiry, so
// the reaper failed a PENDING row whose client held a fresh, valid URL.
// ExtendPendingPresign is the SQL that moves the deadline; these pin what it
// may and may not do against the real schema.
func TestExtendPendingPresign(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)
	repo := adapters.NewPresignRepo(sqlc.New(pool), pool)
	base := time.Now().UTC().Truncate(time.Second)

	t.Run("moves a PENDING row's deadline forward", func(t *testing.T) {
		id := seedPendingObject(t, ctx, pool, f)
		mustExec(t, ctx, pool, `UPDATE objects SET presign_expires_at = $2 WHERE id = $1`, id, base)
		later := base.Add(time.Hour)
		if err := repo.ExtendPendingPresign(ctx, f.tenantID, id, later); err != nil {
			t.Fatal(err)
		}
		if got := presignDeadline(t, ctx, pool, id); got == nil || !got.Equal(later) {
			t.Fatalf("deadline = %v, want %v", got, later)
		}
	})

	// A regenerated URL shorter than one still live must not pull the
	// deadline in under it.
	t.Run("never moves the deadline backwards", func(t *testing.T) {
		id := seedPendingObject(t, ctx, pool, f)
		mustExec(t, ctx, pool, `UPDATE objects SET presign_expires_at = $2 WHERE id = $1`, id, base.Add(2*time.Hour))
		if err := repo.ExtendPendingPresign(ctx, f.tenantID, id, base.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		if got := presignDeadline(t, ctx, pool, id); got == nil || !got.Equal(base.Add(2*time.Hour)) {
			t.Fatalf("deadline = %v, want it kept at %v", got, base.Add(2*time.Hour))
		}
	})

	t.Run("sets a deadline on a row that had none", func(t *testing.T) {
		id := seedPendingObject(t, ctx, pool, f)
		if err := repo.ExtendPendingPresign(ctx, f.tenantID, id, base); err != nil {
			t.Fatal(err)
		}
		if got := presignDeadline(t, ctx, pool, id); got == nil || !got.Equal(base) {
			t.Fatalf("deadline = %v, want %v", got, base)
		}
	})

	// The object left PENDING between the handler's lookup and this update:
	// the URL just signed must not be handed out, so the caller is told.
	t.Run("refuses a row that is no longer PENDING", func(t *testing.T) {
		id := seedPendingObject(t, ctx, pool, f)
		mustExec(t, ctx, pool, `UPDATE objects SET state = 'FAILED', presign_expires_at = $2 WHERE id = $1`, id, base)
		err := repo.ExtendPendingPresign(ctx, f.tenantID, id, base.Add(time.Hour))
		if !errors.Is(err, presignh.ErrNotPending) {
			t.Fatalf("err = %v, want ErrNotPending", err)
		}
		if got := presignDeadline(t, ctx, pool, id); got == nil || !got.Equal(base) {
			t.Fatalf("a terminal row's deadline moved to %v", got)
		}
	})

	t.Run("is scoped to the caller's tenant", func(t *testing.T) {
		id := seedPendingObject(t, ctx, pool, f)
		mustExec(t, ctx, pool, `UPDATE objects SET presign_expires_at = $2 WHERE id = $1`, id, base)
		err := repo.ExtendPendingPresign(ctx, uuid.New(), id, base.Add(time.Hour))
		if !errors.Is(err, presignh.ErrNotPending) {
			t.Fatalf("err = %v, want ErrNotPending for another tenant's object", err)
		}
		if got := presignDeadline(t, ctx, pool, id); got == nil || !got.Equal(base) {
			t.Fatalf("another tenant moved the deadline to %v", got)
		}
	})

	t.Run("LookupObject returns the stored Content-Type", func(t *testing.T) {
		id := seedPendingObject(t, ctx, pool, f)
		ref, err := repo.LookupObject(ctx, f.tenantID, f.collection, id)
		if err != nil {
			t.Fatal(err)
		}
		if ref.State != "PENDING" || ref.ContentType != "application/octet-stream" || ref.Collection != f.collection {
			t.Fatalf("LookupObject = %+v", ref)
		}
	})
}
