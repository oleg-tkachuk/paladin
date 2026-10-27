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
	"uuid"

	"github.com/jackc/pgx/v5/pgxpool"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/dpop"
	"github.com/oleg-tkachuk/limes/dpop/dpoptest"
	"github.com/oleg-tkachuk/limes/jwks"
	"github.com/oleg-tkachuk/limes/memstore"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	capstore "github.com/oleg-tkachuk/paladin/backend/internal/capability/postgres"
)

// The keys and holders the cache tests record proofs of.
const (
	replayKey    = "key-a"
	otherKey     = "key-b"
	replayHolder = "holder-a"
	otherHolder  = "holder-b"
)

const (
	dpopReplayURL    = "https://paladin.example/paladin.v1.ObjectService/GetObject"
	dpopReplayIssuer = "paladin-replay-test"
)

// contractEntriesPerHolder is the holder share the replay cache contract runs
// with: small, so the suite fills it.
const contractEntriesPerHolder = 3

func newReplayCache(t *testing.T, pool *pgxpool.Pool) *capstore.ReplayCache {
	t.Helper()
	c, err := capstore.NewReplayCache(capstore.ReplayCacheConfig{Pool: pool})
	if err != nil {
		t.Fatalf("new replay cache: %v", err)
	}
	return c
}

// The module's replay cache contract, on the application role, on a clock
// the suite moves.
func TestDPoPReplayCacheContract(t *testing.T) {
	dpoptest.RunReplayCache(t, func(t *testing.T) dpoptest.ReplayEnv {
		t.Helper()
		ctx := context.Background()
		clock := newFakeClock()
		cache, err := capstore.NewReplayCache(capstore.ReplayCacheConfig{
			Pool: rlsPool(t, ctx, startPostgres(t)), Now: clock.Now, EntriesPerHolder: contractEntriesPerHolder,
		})
		if err != nil {
			t.Fatal(err)
		}
		return dpoptest.ReplayEnv{
			Ctx: ctx, Cache: cache, Now: clock.Now, Advance: clock.Advance,
			EntriesPerHolder: contractEntriesPerHolder,
		}
	})
}

// A proof accepted through one replica must be refused through another: two
// verifiers, each with its own pool and cache, on one database — and on the
// application role with no tenant in the context, which is how the check runs.
func TestDPoPProofSeenOnOneReplicaIsRefusedOnAnother(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)

	signing, err := limes.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	signer, err := limes.NewSigner(limes.SignerConfig{KeyID: signing.KeyID, Key: signing.Private})
	if err != nil {
		t.Fatal(err)
	}
	store, err := memstore.NewStore(memstore.StoreConfig{})
	if err != nil {
		t.Fatal(err)
	}
	issuer, err := limes.NewIssuer(limes.IssuerConfig{Signer: signer, Store: store, IssuerName: dpopReplayIssuer})
	if err != nil {
		t.Fatal(err)
	}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	jkt, err := dpop.Thumbprint(key.Public())
	if err != nil {
		t.Fatal(err)
	}
	tenant := uuid.New()
	res, err := issuer.Issue(ctx, limes.IssueRequest{
		IssuedBy:        limes.Principal{Type: limes.PrincipalTypeUser, TenantID: tenant, Subject: "op"},
		Subject:         limes.Principal{Type: limes.PrincipalTypeAgent, TenantID: tenant, Subject: "agent"},
		Audience:        []string{auth.TokenPlaneData},
		Caveats:         limes.Caveats{Ops: []limes.Op{limes.OpGet}, Requests: limes.RequestCeiling{Unlimited: true}, Budget: limes.BudgetCeiling{Unlimited: true, UnitCode: testUnit}},
		TTL:             time.Hour,
		ConfirmationJKT: jkt,
	})
	if err != nil {
		t.Fatal(err)
	}
	keys, err := jwks.NewStatic(jwks.StaticConfig{Keys: map[string]ed25519.PublicKey{signing.KeyID: signing.Public()}})
	if err != nil {
		t.Fatal(err)
	}
	replica := func() *limes.Verifier {
		proofs, err := dpop.NewVerifier(dpop.VerifierConfig{Replay: newReplayCache(t, rlsPool(t, ctx, admin))})
		if err != nil {
			t.Fatal(err)
		}
		v, err := limes.NewVerifier(limes.VerifierConfig{
			Keys: keys, Revocations: store, TrustedIssuers: []string{dpopReplayIssuer}, DPoP: proofs,
		})
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	first, second := replica(), replica()

	proof, err := dpop.Sign(ctx, key, dpop.SignRequest{
		Method: http.MethodPost, URL: dpopReplayURL, Token: res.Token, IssuedAt: time.Now(),
	})
	if err != nil {
		t.Fatal(err)
	}
	req := limes.VerifyRequest{
		Token:    res.Token,
		Audience: auth.TokenPlaneData,
		Scheme:   limes.AuthSchemeDPoP,
		DPoP:     dpop.Proof{JWT: proof, Method: http.MethodPost, URL: dpopReplayURL},
	}

	if _, err := first.Verify(ctx, req); err != nil {
		t.Fatalf("first use of the proof: %v", err)
	}
	if _, err := second.Verify(ctx, req); !errors.Is(err, dpop.ErrReplayed) {
		t.Fatalf("the proof replayed on another replica: err = %v, want dpop.ErrReplayed", err)
	}
	if _, err := first.Verify(ctx, req); !errors.Is(err, dpop.ErrReplayed) {
		t.Fatalf("the proof replayed on the same replica: err = %v, want dpop.ErrReplayed", err)
	}
}

// seen records a proof id of replayHolder, failing the test on an error.
func seen(t *testing.T, cache *capstore.ReplayCache, jkt, jti string, expires time.Time) bool {
	t.Helper()
	replayed, err := cache.Seen(context.Background(), dpop.ReplayEntry{JKT: jkt, JTI: jti, Holder: replayHolder, ExpiresAt: expires})
	if err != nil {
		t.Fatalf("seen %s/%s: %v", jkt, jti, err)
	}
	return replayed
}

// The purger removes expired records and only those.
func TestDPoPReplayCachePurgesExpiredIDs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	admin := startPostgres(t)
	cache := newReplayCache(t, rlsPool(t, ctx, admin))
	past, future := time.Now().Add(-time.Minute), time.Now().Add(time.Hour)

	if seen(t, cache, replayKey, "stale", past) || seen(t, cache, replayKey, "live", future) {
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
	rows, err := admin.Query(ctx, `SELECT jti FROM dpop_seen_proofs ORDER BY jti`)
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
	if len(left) != 1 || left[0] != "live" {
		t.Fatalf("after purge: %v, want [live]", left)
	}
}

// A holder past its share is refused and logged once as such; a replay is
// refused without that warning, and another holder records as before.
func TestDPoPReplayCacheWarnsOfAHolderPastItsShare(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	core, logs := observer.New(zapcore.WarnLevel)
	cache, err := capstore.NewReplayCache(capstore.ReplayCacheConfig{
		Pool: rlsPool(t, ctx, startPostgres(t)), Log: zap.New(core), EntriesPerHolder: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	future := time.Now().Add(time.Hour)
	if seen(t, cache, replayKey, "first", future) {
		t.Fatal("a new id reported seen")
	}
	if !seen(t, cache, otherKey, "one-more", future) {
		t.Error("a holder past its share recorded a new proof id under another key")
	}
	if n := logs.FilterMessageSnippet("past its share").FilterField(zap.String("holder", replayHolder)).Len(); n != 1 {
		t.Errorf("%d warnings name the full holder, want 1", n)
	}
	if !seen(t, cache, replayKey, "first", future) {
		t.Error("a replayed proof id was recorded again")
	}
	if n := logs.Len(); n != 1 {
		t.Errorf("%d warnings, want 1: a replay is not a holder past its share", n)
	}
	replayed, err := cache.Seen(ctx, dpop.ReplayEntry{JKT: otherKey, JTI: "first", Holder: otherHolder, ExpiresAt: future})
	if err != nil || replayed {
		t.Errorf("another holder's new proof id: replayed %t, err %v", replayed, err)
	}
}

// A check the database cannot answer refuses the proof and says why.
func TestDPoPReplayCacheFailsClosed(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := rlsPool(t, ctx, startPostgres(t))
	cache := newReplayCache(t, pool)
	pool.Close()

	replayed, err := cache.Seen(ctx, dpop.ReplayEntry{JKT: replayKey, JTI: "unrecordable", Holder: replayHolder, ExpiresAt: time.Now().Add(time.Minute)})
	if !replayed || err == nil {
		t.Fatalf("a proof id that could not be recorded: replayed %t, err %v; want refused with an error", replayed, err)
	}
}
