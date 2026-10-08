//go:build integration

package components

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	capstore "github.com/oleg-tkachuk/paladin/backend/internal/capability/postgres"
	"github.com/oleg-tkachuk/paladin/capability"
)

const (
	dpopReplayURL   = "https://paladin.example/paladin.v1.ObjectService/GetObject"
	dpopReplayToken = "capability-token"
)

func newReplayCache(t *testing.T, pool *pgxpool.Pool) *capstore.ReplayCache {
	t.Helper()
	c, err := capstore.NewReplayCache(pool, nil)
	if err != nil {
		t.Fatalf("new replay cache: %v", err)
	}
	return c
}

// A proof accepted through one replica must be refused through another: two
// verifiers, each with its own pool and cache, on one database — and on the
// application role with no tenant in the context, which is how the check runs.
func TestDPoPProofSeenOnOneReplicaIsRefusedOnAnother(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)

	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	jkt, err := capability.KeyThumbprint(key.Public())
	if err != nil {
		t.Fatal(err)
	}
	c := &capability.Capability{ID: uuid.New(), ConfirmationJKT: jkt}
	proof, err := capability.NewDPoPProof(key, "POST", dpopReplayURL, dpopReplayToken, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	req := capability.DPoPRequest{Proof: proof, Method: "POST", URL: dpopReplayURL, Token: dpopReplayToken}

	first := &capability.DPoPVerifier{Replay: newReplayCache(t, rlsPool(t, ctx, admin))}
	second := &capability.DPoPVerifier{Replay: newReplayCache(t, rlsPool(t, ctx, admin))}

	if err := first.Check(ctx, *c, req); err != nil {
		t.Fatalf("first use of the proof: %v", err)
	}
	if err := second.Check(ctx, *c, req); !errors.Is(err, capability.ErrDPoPReplayed) {
		t.Fatalf("the proof replayed on another replica: err = %v, want ErrDPoPReplayed", err)
	}
	if err := first.Check(ctx, *c, req); !errors.Is(err, capability.ErrDPoPReplayed) {
		t.Fatalf("the proof replayed on the same replica: err = %v, want ErrDPoPReplayed", err)
	}
}

// An id whose record has expired is the same as an unseen one, whether or
// not the purger has run yet; the purger removes expired records and only
// those.
func TestDPoPReplayCacheForgetsExpiredIDs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)
	cache := newReplayCache(t, rlsPool(t, ctx, admin))
	past, future := time.Now().Add(-time.Minute), time.Now().Add(time.Hour)

	if cache.Seen(ctx, "expired", past) {
		t.Fatal("a new id reported seen")
	}
	if cache.Seen(ctx, "expired", future) {
		t.Fatal("an id whose record expired, not yet purged, reported seen")
	}
	if !cache.Seen(ctx, "expired", future) {
		t.Fatal("the overwritten record does not hold")
	}

	if cache.Seen(ctx, "stale", past) || cache.Seen(ctx, "live", future) {
		t.Fatal("a new id reported seen")
	}
	n, err := cache.PurgeExpired(ctx)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n != 1 {
		t.Fatalf("purged %d ids, want 1 (only the expired one)", n)
	}
	var left []string
	rows, err := admin.Query(ctx, `SELECT jti FROM dpop_seen_jti ORDER BY jti`)
	if err != nil {
		t.Fatal(err)
	}
	for rows.Next() {
		var jti string
		if err := rows.Scan(&jti); err != nil {
			t.Fatal(err)
		}
		left = append(left, jti)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if len(left) != 2 || left[0] != "expired" || left[1] != "live" {
		t.Fatalf("after purge: %v, want [expired live]", left)
	}
}

// A check the database cannot answer refuses the proof.
func TestDPoPReplayCacheFailsClosed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := rlsPool(t, ctx, startPostgres(t))
	cache := newReplayCache(t, pool)
	pool.Close()

	if !cache.Seen(ctx, "unrecordable", time.Now().Add(time.Minute)) {
		t.Fatal("a proof id that could not be recorded was accepted")
	}
}
