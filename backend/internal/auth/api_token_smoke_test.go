package auth

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/emptypb"

	"github.com/oleg-tkachuk/paladin/internal/auth/api_token"
	"github.com/oleg-tkachuk/paladin/internal/auth/api_token/ratelimit"
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

func (denyingLimiter) Allow(context.Context, uuid.UUID, int) (ratelimit.Decision, error) {
	return ratelimit.Decision{
		Allowed:       false,
		WeightedCount: 999,
		RetryAfter:    30 * time.Second,
	}, nil
}
func (denyingLimiter) Usage(context.Context, uuid.UUID) (ratelimit.Snapshot, error) {
	return ratelimit.Snapshot{}, nil
}
func (denyingLimiter) Sweep(context.Context, time.Duration) (int64, error) { return 0, nil }

// TestRetryAfter_E2E_ConnectTransport boots a real httptest.Server
// hosting a Connect handler wrapped with APITokenInterceptor +
// denyingLimiter, then calls the endpoint through a real Connect
// client. Confirms that:
//
//   - the rate-limit denial round-trips as connect.CodeResourceExhausted
//   - the Retry-After header set on err.Meta() inside the interceptor
//     actually lands on the HTTP response and is observable on the
//     client-side *connect.Error
//
// The unit test in api_token_ratelimit_test.go covers the same logic
// at the function level; this test is the load-bearing proof that
// the Connect transport propagates Meta to the wire.
func TestRetryAfter_E2E_ConnectTransport(t *testing.T) {
	t.Parallel()

	store := newSmokeStore()
	hasher, err := api_token.NewHasher([]byte("smoke-test-hmac-key-0123456789-abc"))
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

	interceptor := APITokenInterceptorWithLimiter(verifier, denyingLimiter{}, "data")

	const procedure = "/auth.smoke.SmokeService/Echo"
	echo := func(_ context.Context, req *connect.Request[emptypb.Empty]) (*connect.Response[emptypb.Empty], error) {
		// We expect this to never run — the interceptor short-circuits
		// before reaching here. Failing the test if it does keeps the
		// behavioural contract honest if we ever break the gate order.
		return connect.NewResponse(&emptypb.Empty{}), nil
	}
	h := connect.NewUnaryHandler(procedure, echo, connect.WithInterceptors(interceptor))

	mux := http.NewServeMux()
	mux.Handle(procedure, h)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	// Mint a real token through the issuer so the verifier accepts
	// it on the server side. RateLimitRPM > 0 so the gate runs.
	tok, err := issuer.Issue(context.Background(), api_token.IssueRequest{
		TenantID:     uuid.New(),
		Name:         "smoke",
		Audience:     []string{"data"},
		TTL:          time.Hour,
		RateLimitRPM: 60,
	})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	client := connect.NewClient[emptypb.Empty, emptypb.Empty](http.DefaultClient, srv.URL+procedure)
	req := connect.NewRequest(&emptypb.Empty{})
	req.Header().Set("Authorization", "Bearer "+tok.Plaintext)

	_, err = client.CallUnary(context.Background(), req)
	if err == nil {
		t.Fatal("expected denial error from interceptor, got nil")
	}

	var ce *connect.Error
	if !errors.As(err, &ce) {
		t.Fatalf("expected *connect.Error, got %T: %v", err, err)
	}
	if ce.Code() != connect.CodeResourceExhausted {
		t.Errorf("code: got %v, want CodeResourceExhausted", ce.Code())
	}
	if got := ce.Meta().Get("Retry-After"); got != "30" {
		t.Errorf("Retry-After: got %q, want %q", got, "30")
	}
}

// (A raw-HTTP variant — POST with hand-crafted Connect-protocol body —
// would prove the byte-level header path independently of how
// connect-go's client decodes errors. Not added here because crafting
// a valid Connect request body for emptypb.Empty without the
// generated client just to observe a header would obscure the test.
// The connect.Client test above already exercises the real transport;
// the *connect.Error.Meta() it surfaces is connect-go's view of the
// HTTP response headers it received over the wire.)
