//go:build integration

package components

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/adapters"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// mismatchProber reports a stored object of a size the test chooses and
// records deletes.
type mismatchProber struct {
	size      int64
	checksum  string
	deleteErr error

	mu      sync.Mutex
	deleted []string
	algos   []string
}

func (p *mismatchProber) Head(_ context.Context, _, _ string, _ uuid.UUID, _, _, algo string) (string, int64, string, string, error) {
	p.mu.Lock()
	p.algos = append(p.algos, algo)
	p.mu.Unlock()
	return "etag", p.size, p.checksum, "", nil
}

func (p *mismatchProber) DeleteObject(_ context.Context, _, _ string, _ uuid.UUID, collection, key string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.deleted = append(p.deleted, collection+"/"+key)
	return p.deleteErr
}

// The reconciler promoted whatever a HEAD reported. An object registered at
// 10 bytes whose stored bytes are 11 — a backend that did not enforce the
// signed Content-Length — was made AVAILABLE at the wrong size. It now
// deletes those bytes and fails the row.
func TestReconcilerDiscardsBytesThatBreakTheRegistration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)

	id := uuid.Must(uuid.NewV7())
	mustExec(t, ctx, pool, `
		INSERT INTO objects (id, tenant_id, collection_id, path, state, content_type,
		                     size_bytes, checksum_algorithm, checksum, presign_expires_at)
		SELECT $1, $2, c.id, 'k-wrong', 'PENDING', 'text/plain', 10, 2,
		       '47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=', now() - interval '48 hours'
		  FROM collections c WHERE c.tenant_id = $2 AND c.name = $3`,
		id, f.tenantID, f.collection)

	prober := &mismatchProber{size: 11}
	runOneTick(t, adapters.NewReconcilerProbe(sqlc.New(pool), prober), statemachine.New(pool))

	if got := objectState(t, ctx, pool, id); got != "FAILED" {
		t.Fatalf("state = %s, want FAILED", got)
	}
	prober.mu.Lock()
	defer prober.mu.Unlock()
	if len(prober.deleted) == 0 || prober.deleted[0] != f.collection+"/k-wrong" {
		t.Fatalf("deleted %v, want the mismatched bytes", prober.deleted)
	}
	// HEAD was asked for the object's own algorithm.
	if len(prober.algos) == 0 || prober.algos[0] != "SHA256" {
		t.Fatalf("HEAD asked for %v, want SHA256", prober.algos)
	}
}

// A checksum the store reports that differs from the registered one is the
// same breach.
func TestReconcilerDiscardsAChecksumMismatch(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)

	id := uuid.Must(uuid.NewV7())
	mustExec(t, ctx, pool, `
		INSERT INTO objects (id, tenant_id, collection_id, path, state, content_type,
		                     size_bytes, checksum_algorithm, checksum, presign_expires_at)
		SELECT $1, $2, c.id, 'k-sum', 'PENDING', 'text/plain', 10, 2,
		       '47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=', now() - interval '48 hours'
		  FROM collections c WHERE c.tenant_id = $2 AND c.name = $3`,
		id, f.tenantID, f.collection)

	prober := &mismatchProber{size: 10, checksum: "ZIdsV7fkVDpbmwOztBQpJTYMtaHSYOPwqP2wlOYsrdU="}
	runOneTick(t, adapters.NewReconcilerProbe(sqlc.New(pool), prober), statemachine.New(pool))

	if got := objectState(t, ctx, pool, id); got != "FAILED" {
		t.Fatalf("state = %s, want FAILED", got)
	}
}

// When the bytes cannot be deleted the row stays PENDING: a FAILED row is
// never revisited, so failing it would leave the bytes stored for good.
func TestReconcilerKeepsAMismatchPendingWhileItsBytesRemain(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)

	id := uuid.Must(uuid.NewV7())
	mustExec(t, ctx, pool, `
		INSERT INTO objects (id, tenant_id, collection_id, path, state, content_type,
		                     size_bytes, checksum_algorithm, presign_expires_at)
		SELECT $1, $2, c.id, 'k-stuck', 'PENDING', 'text/plain', 10, 2, now() - interval '48 hours'
		  FROM collections c WHERE c.tenant_id = $2 AND c.name = $3`,
		id, f.tenantID, f.collection)

	prober := &mismatchProber{size: 99, deleteErr: errors.New("s3 down")}
	runOneTick(t, adapters.NewReconcilerProbe(sqlc.New(pool), prober), statemachine.New(pool))

	if got := objectState(t, ctx, pool, id); got != "PENDING" {
		t.Fatalf("state = %s, want PENDING while the bytes remain", got)
	}
}

// A matching object is promoted as before, with the registered checksum kept
// when the store reports none.
func TestReconcilerPromotesBytesThatMatchTheRegistration(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := startPostgres(t)
	f := seedFixture(t, ctx, pool)

	id := uuid.Must(uuid.NewV7())
	mustExec(t, ctx, pool, `
		INSERT INTO objects (id, tenant_id, collection_id, path, state, content_type,
		                     size_bytes, checksum_algorithm, checksum, presign_expires_at)
		SELECT $1, $2, c.id, 'k-right', 'PENDING', 'text/plain', 10, 2,
		       '47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=', now() - interval '48 hours'
		  FROM collections c WHERE c.tenant_id = $2 AND c.name = $3`,
		id, f.tenantID, f.collection)

	prober := &mismatchProber{size: 10}
	runOneTick(t, adapters.NewReconcilerProbe(sqlc.New(pool), prober), statemachine.New(pool))

	if got := objectState(t, ctx, pool, id); got != "AVAILABLE" {
		t.Fatalf("state = %s, want AVAILABLE", got)
	}
	var sum string
	if err := pool.QueryRow(ctx, `SELECT checksum FROM objects WHERE id = $1`, id).Scan(&sum); err != nil {
		t.Fatal(err)
	}
	if sum != "47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU=" {
		t.Fatalf("checksum = %q, want the registered one kept", sum)
	}
	prober.mu.Lock()
	defer prober.mu.Unlock()
	if len(prober.deleted) != 0 {
		t.Fatalf("deleted %v from a matching object", prober.deleted)
	}
}
