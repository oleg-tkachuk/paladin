package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary/unarytest"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/api_token"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/api_token/ratelimit"
	iamv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1/paladiniamv1connect"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// smokeStore is the inline Store used by the end-to-end smoke test.
// Lives in this file (not the api_token unit-test memStore) because
// these tests live in package `auth` rather than package `api_token`,
// and crossing the package boundary just to reuse a stub costs more
// than redeclaring it here.
type smokeStore struct {
	mu       sync.RWMutex
	rows     map[uuid.UUID]api_token.Token
	byDigest map[string]uuid.UUID
}

func newSmokeStore() *smokeStore {
	return &smokeStore{
		rows:     map[uuid.UUID]api_token.Token{},
		byDigest: map[string]uuid.UUID{},
	}
}

func (s *smokeStore) Insert(_ context.Context, t api_token.Token, digest []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t.Plaintext = ""
	s.rows[t.ID] = t
	s.byDigest[string(digest)] = t.ID
	return nil
}
func (s *smokeStore) FindByDigest(_ context.Context, digest []byte) (api_token.Token, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	id, ok := s.byDigest[string(digest)]
	if !ok {
		return api_token.Token{}, api_token.ErrTokenNotFound
	}
	return s.rows[id], nil
}
func (s *smokeStore) Get(_ context.Context, id uuid.UUID) (api_token.Token, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	t, ok := s.rows[id]
	if !ok {
		return api_token.Token{}, api_token.ErrTokenNotFound
	}
	return t, nil
}
func (s *smokeStore) Revoke(context.Context, uuid.UUID) error { return nil }
func (s *smokeStore) TouchLastUsed(context.Context, uuid.UUID, uuid.UUID, time.Time) error {
	return nil
}
func (s *smokeStore) ListByTenant(context.Context, api_token.ListByTenantArgs) ([]api_token.Token, string, error) {
	return nil, "", nil
}
func (s *smokeStore) PurgeExpired(context.Context, time.Duration) (int64, error) { return 0, nil }

// denyingLimiter forces every Allow call to return a denial with a
// fixed Retry-After. Distinct from the recordingLimiter in
// api_token_ratelimit_test.go (which configures Decision per-test);
// this one is purpose-built for the E2E case where we need a
// deterministic "always deny with 30s retry-after".
type denyingLimiter struct{}

// denyingRetryAfter is the wait every denyingLimiter decision asks for.
const denyingRetryAfter = 30 * time.Second

func (denyingLimiter) Allow(context.Context, uuid.UUID, int) (ratelimit.Decision, error) {
	const farOverAnyLimit = 999
	return ratelimit.Decision{
		Allowed:       false,
		WeightedCount: farOverAnyLimit,
		RetryAfter:    denyingRetryAfter,
	}, nil
}
func (denyingLimiter) Usage(context.Context, uuid.UUID) (ratelimit.Snapshot, error) {
	return ratelimit.Snapshot{}, nil
}
func (denyingLimiter) Sweep(context.Context, time.Duration) (int64, error) { return 0, nil }

// TestRetryAfter_E2E_ConnectTransport boots a real httptest.Server
// hosting a Connect server wrapped with APITokenInterceptor +
// denyingLimiter, then calls the endpoint through a real Connect
// client. Confirms that:
//
//   - the rate-limit denial round-trips as connect.CodeResourceExhausted
//   - the Retry-After header the interceptor sets on the call's response
//     header actually lands on the HTTP response of a failed call and is
//     observable on the client side
//
// The unit test in api_token_ratelimit_test.go covers the same logic
// at the function level; this test is the load-bearing proof that
// the Connect transport carries the header of an error response to the
// wire.
func TestRetryAfter_E2E_ConnectTransport(t *testing.T) {
	t.Parallel()

	const (
		hmacKey = "smoke-test-hmac-key-0123456789-abc"
		rpm     = 60
	)
	store := newSmokeStore()
	hasher, err := api_token.NewHasher([]byte(hmacKey))
	if err != nil {
		t.Fatalf("hasher: %v", err)
	}
	issuer, err := api_token.NewIssuer(api_token.IssuerConfig{
		Store:  store,
		Hasher: hasher,
		MaxTTL: time.Hour,
	})
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}
	verifier, err := api_token.NewVerifier(api_token.VerifierConfig{Store: store, Hasher: hasher})
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}

	interceptor := APITokenInterceptorWithLimiter(verifier, denyingLimiter{}, planeData)

	// We expect the handler to never run — the interceptor short-circuits
	// before reaching it. Failing the test if it does keeps the
	// behavioural contract honest if we ever break the gate order.
	var handlerRan atomic.Bool
	probe := &unarytest.Probe{OnCall: func(context.Context) error {
		handlerRan.Store(true)
		return nil
	}}
	server := connect.NewServer(interceptor)
	paladiniamv1connect.RegisterHealthServiceHandler(server, probe)
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Mint a real token through the issuer so the verifier accepts
	// it on the server side. RateLimitRPM > 0 so the gate runs.
	tok, err := issuer.Issue(context.Background(), api_token.IssueRequest{
		TenantID:     uuid.New(),
		Name:         "smoke",
		Audience:     []string{planeData},
		TTL:          time.Hour,
		RateLimitRPM: rpm,
	})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	client := paladiniamv1connect.NewHealthServiceClient(
		connect.NewClient(connecthttp.NewTransport(srv.Client(), srv.URL)))
	ctx, info := connect.NewClientContext(context.Background())
	info.RequestHeader().Set(paladin.HeaderAuthorization, bearerPrefix+tok.Plaintext)

	_, err = client.GetVersion(ctx, &iamv1.GetVersionRequest{})
	if err == nil {
		t.Fatal("expected denial error from interceptor, got nil")
	}
	if handlerRan.Load() {
		t.Error("the handler ran despite the rate-limit denial")
	}

	var ce *connect.Error
	if !errors.As(err, &ce) {
		t.Fatalf("expected *connect.Error, got %T: %v", err, err)
	}
	if ce.Code() != connect.CodeResourceExhausted {
		t.Errorf("code: got %v, want CodeResourceExhausted", ce.Code())
	}
	want := strconv.Itoa(int(denyingRetryAfter.Seconds()))
	if got := info.ResponseHeader().Get(paladin.HeaderRetryAfter); got != want {
		t.Errorf("%s: got %q, want %q", paladin.HeaderRetryAfter, got, want)
	}
}

// (A raw-HTTP variant — POST with hand-crafted Connect-protocol body —
// would prove the byte-level header path independently of how
// connect-go's client decodes responses. Not added here because crafting
// a valid Connect request body without the generated client just to
// observe a header would obscure the test. The client call above already
// exercises the real transport; the response header it reports is
// connect-go's view of the HTTP response headers it received over the
// wire.)
