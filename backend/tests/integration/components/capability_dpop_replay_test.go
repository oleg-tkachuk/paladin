//go:build integration

package components

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/memstore"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	capstore "github.com/oleg-tkachuk/paladin/backend/internal/capability/postgres"
)

const (
	dpopReplayURL    = "https://paladin.example/paladin.v1.ObjectService/GetObject"
	dpopReplayIssuer = "paladin-replay-test"
	dpopReplayKID    = "k1"
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

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := limes.NewEd25519Signer(dpopReplayKID, priv)
	if err != nil {
		t.Fatal(err)
	}
	store := memstore.New[any]()
	issuer, err := limes.NewIssuer(limes.IssuerConfig{Signer: signer, Store: store, IssuerName: dpopReplayIssuer})
	if err != nil {
		t.Fatal(err)
	}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	jkt, err := limes.KeyThumbprint(key.Public())
	if err != nil {
		t.Fatal(err)
	}
	_, token, err := issuer.Issue(ctx, limes.IssueRequest{
		IssuedBy:        limes.Principal{Subject: "op"},
		Subject:         limes.Principal{TenantID: uuid.New(), Subject: "agent"},
		Audience:        []string{auth.TokenPlaneData},
		Caveats:         limes.Caveats{Ops: []limes.Op{limes.OpGet}},
		TTL:             time.Hour,
		ConfirmationJKT: jkt,
	})
	if err != nil {
		t.Fatal(err)
	}
	replica := func() *limes.StandardVerifier {
		v, err := limes.NewStandardVerifier(limes.VerifierConfig{
			Keys:           limes.NewStaticKeyResolver(map[string]ed25519.PublicKey{dpopReplayKID: pub}),
			Revocations:    store,
			TrustedIssuers: []string{dpopReplayIssuer},
			DPoP:           &limes.DPoPVerifier{Replay: newReplayCache(t, rlsPool(t, ctx, admin))},
		})
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	first, second := replica(), replica()

	proof, err := limes.NewDPoPProof(key, limes.DPoPProofRequest{
		Method: http.MethodPost, URL: dpopReplayURL, Token: token, Now: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	req := limes.VerifyRequest{
		Token:    token,
		Audience: auth.TokenPlaneData,
		DPoP:     limes.DPoPProof{Proof: proof, Method: http.MethodPost, URL: dpopReplayURL},
	}

	if _, err := first.Verify(ctx, req); err != nil {
		t.Fatalf("first use of the proof: %v", err)
	}
	if _, err := second.Verify(ctx, req); !errors.Is(err, limes.ErrDPoPReplayed) {
		t.Fatalf("the proof replayed on another replica: err = %v, want ErrDPoPReplayed", err)
	}
	if _, err := first.Verify(ctx, req); !errors.Is(err, limes.ErrDPoPReplayed) {
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
