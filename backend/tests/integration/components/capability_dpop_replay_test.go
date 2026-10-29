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

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/dpop"
	"github.com/oleg-tkachuk/limes/dpop/dpoptest"
	"github.com/oleg-tkachuk/limes/jwks"
	"github.com/oleg-tkachuk/limes/memstore"
	"github.com/oleg-tkachuk/limes/pgstore"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/captest"
)

const (
	dpopReplayURL    = "https://paladin.example/paladin.v1.ObjectService/GetObject"
	dpopReplayIssuer = "paladin-replay-test"
)

// contractEntriesPerHolder is the holder share the replay cache contract runs
// with: small, so the suite fills it.
const contractEntriesPerHolder = 3

func newReplayCache(t *testing.T, pool *pgxpool.Pool) *pgstore.ReplayCache {
	t.Helper()
	c, err := pgstore.NewReplayCache(pgstore.ReplayConfig{DB: pool})
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
		cache, err := pgstore.NewReplayCache(pgstore.ReplayConfig{
			DB: rlsPool(t, ctx, startPostgres(t)), Now: clock.Now, EntriesPerHolder: contractEntriesPerHolder,
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
		ClientID:        captest.ClientID,
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

// The capability purger sweeps the replay cache on the application role:
// pgstore's migration grants it the delete, and an expired id goes while a
// live one stays.
func TestDPoPReplayCachePurgesOnTheApplicationRole(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	clock := newFakeClock()
	cache, err := pgstore.NewReplayCache(pgstore.ReplayConfig{DB: rlsPool(t, ctx, startPostgres(t)), Now: clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	record := func(jti string) {
		t.Helper()
		replayed, err := cache.Seen(ctx, dpop.ReplayEntry{JKT: "key", JTI: jti, Holder: "holder", ExpiresAt: clock.Now().Add(dpop.DefaultWindow)})
		if err != nil || replayed {
			t.Fatalf("record %s: replayed %t, err %v", jti, replayed, err)
		}
	}
	record("stale")
	clock.Advance(2 * dpop.DefaultWindow)
	record("live")

	n, err := cache.PurgeExpired(ctx)
	if err != nil {
		t.Fatalf("purge: %v", err)
	}
	if n != 1 {
		t.Fatalf("purged %d ids, want 1 (only the expired one)", n)
	}
}
