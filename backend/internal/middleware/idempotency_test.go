package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	iamv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1/paladiniamv1connect"
)

// TestIsMutationMethod pins the Connect-procedure-name parser
// used by RequireOnCreate (the field is named for historical
// reasons; matches both Create AND Issue prefixes). The risky
// cases are:
//   - service-prefix containing "Create" or "Issue" (must NOT
//     match — we scope to the trailing method segment, otherwise
//     a hypothetical CreateOrderHistoryService/ListOrders or
//     IssueTrackerService/ListIssues would be force-gated),
//   - empty / malformed input (defensive),
//   - non-mutation verbs (Update/Delete/Get/List) — these have
//     their own deduplication shape (resource_version for OCC,
//     idempotent-by-construction for reads) and don't go through
//     the Idempotency-Key gate.
func TestIsMutationMethod(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		// Create-prefixed: covered.
		{"create tenant", "/paladin.admin.v1.TenantService/CreateTenant", true},
		{"create bucket", "/paladin.admin.v1.BucketService/CreateBucket", true},
		// Issue-prefixed: the Capability variant added to make the
		// CapabilityService.Issue double-submit observable via
		// FR-008.
		{"issue capability", "/paladin.admin.v1.CapabilityService/Issue", true},
		{"issue (composed verb)", "/paladin.admin.v1.SomeService/IssueToken", true},
		// Negatives.
		{"list is not mutation", "/paladin.admin.v1.TenantService/ListTenants", false},
		{"update is not mutation", "/paladin.admin.v1.TenantService/UpdateTenant", false},
		{"delete is not mutation", "/paladin.admin.v1.TenantService/DeleteTenant", false},
		{"service prefix containing create", "/paladin.admin.v1.CreateOrderHistoryService/ListOrders", false},
		{"service prefix containing issue", "/paladin.admin.v1.IssueTrackerService/ListIssues", false},
		// Defensive.
		{"empty", "", false},
		{"no slash", "Create", false},
		{"trailing slash", "/svc/", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := isMutationMethod(tc.in); got != tc.want {
				t.Fatalf("isMutationMethod(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestReconstructResponse exercises the reflection round-trip:
// marshal a VersionInfo, throw away the live response wrapper, then
// rebuild a fresh *connect.Response[VersionInfo] from just the bytes
// and the wrapper's reflect.Type. The reconstructed value must
//   - satisfy connect.AnyResponse (the interceptor returns it as-is),
//   - expose the same proto fields via Any().
//
// This is the core trick the BACKLOG had previously labeled
// "architecturally impossible". The trick works because
// `internalOnly()` is a method on *Response[_], not an instance
// secret — reflect.New() produces a value whose dynamic type carries
// the same method set.
func TestReconstructResponse(t *testing.T) {
	original := connect.NewResponse(&iamv1.VersionInfo{
		Version:   "1.2.3",
		Commit:    "abc1234",
		GoVersion: "go1.26.0",
	})
	bytes, err := proto.Marshal(original.Msg)
	if err != nil {
		t.Fatalf("proto.Marshal: %v", err)
	}

	respType := reflect.TypeOf(original)
	any, err := reconstructResponse(respType, bytes)
	if err != nil {
		t.Fatalf("reconstructResponse: %v", err)
	}

	// The reconstructed value's dynamic type must be the same as
	// the original wrapper — otherwise the Connect serializer would
	// blow up at write-time (wrong T).
	if got, want := reflect.TypeOf(any), respType; got != want {
		t.Fatalf("reconstructed type = %v, want %v", got, want)
	}

	got, ok := any.Any().(*iamv1.VersionInfo)
	if !ok {
		t.Fatalf("reconstructed Any() = %T, want *iamv1.VersionInfo", any.Any())
	}
	if got.Version != "1.2.3" || got.Commit != "abc1234" || got.GoVersion != "go1.26.0" {
		t.Fatalf("reconstructed payload mismatch: %+v", got)
	}
}

// memStore is an in-memory IdempotencyStore for the integration
// tests below. Mirrors the postgres adapter's contract exactly
// (Get returns found=false on missing, Put is upsert with
// last-writer-wins semantics — slightly looser than ON CONFLICT DO
// NOTHING but enough to verify the interceptor sequence).
type memStore struct {
	mu   sync.Mutex
	data map[string]memEntry
}

type memEntry = IdempotencyRecord

func newMemStore() *memStore { return &memStore{data: make(map[string]memEntry)} }

func (s *memStore) key(tenantID uuid.UUID, method, k string) string {
	return tenantID.String() + "|" + method + "|" + k
}

func (s *memStore) Get(_ context.Context, tenantID uuid.UUID, method, k string) (IdempotencyRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.data[s.key(tenantID, method, k)]
	return e, ok, nil
}

func (s *memStore) Put(_ context.Context, tenantID uuid.UUID, method, k string, rec IdempotencyRecord, _ time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.data[s.key(tenantID, method, k)] = rec
	return nil
}

// stubSystem drives the memoize tests through UserSettingsService.UpdateMine,
// with a per-call counter so they can assert how often the handler ran.
//
// It used to drive them through HealthService.GetVersion, and that stopped
// working the moment GetVersion was annotated NO_SIDE_EFFECTS: the interceptor
// now refuses to memoize a declared read, which is the feature. One test failed
// honestly. TWO others — no-header and skip-methods — went on passing, because
// "the handler ran twice" was suddenly true for a reason that had nothing to do
// with what they assert. A green test whose mechanism is bypassed is worse than
// a red one.
//
// UpdateMine is chosen for the property that broke the old choice: it is a
// write, so it will never be annotated a read, and these tests cannot be
// silently disarmed by the annotation work advancing.
type stubSystem struct {
	paladiniamv1connect.UnimplementedUserSettingsServiceHandler
	mu       sync.Mutex
	calls    int
	failOnce bool
}

func (s *stubSystem) UpdateMine(_ context.Context, _ *connect.Request[iamv1.UpdateMineRequest]) (*connect.Response[iamv1.UserSettings], error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.failOnce {
		s.failOnce = false
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("transient"))
	}
	return connect.NewResponse(&iamv1.UserSettings{
		Name:     "users/me/settings",
		Timezone: "Europe/Kyiv",
	}), nil
}

// principalInjector wraps the chain with an interceptor that
// stamps a fixed Principal into the context. The idempotency
// interceptor needs a tenant; in production that comes from the
// auth.Interceptor upstream. For the unit-test plane we inline a
// minimal stand-in.
func principalInjector(tenantID uuid.UUID) connect.Interceptor {
	return connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			ctx = auth.WithPrincipal(ctx, &auth.Principal{
				TenantID: tenantID,
				Subject:  "test",
				Audience: "paladin-iam",
			})
			return next(ctx, req)
		}
	})
}

func newSystemServer(t *testing.T, store IdempotencyStore, cfg IdempotencyConfig) (paladiniamv1connect.UserSettingsServiceClient, *stubSystem, func()) {
	t.Helper()
	tenant := uuid.New()
	svc := &stubSystem{}
	mux := http.NewServeMux()
	// Order: principal injector must run BEFORE the idempotency
	// interceptor so tenantID is in ctx when Get/Put are called.
	// Connect's WithInterceptors applies in order — first listed
	// is outermost.
	path, handler := paladiniamv1connect.NewUserSettingsServiceHandler(svc,
		connect.WithInterceptors(
			principalInjector(tenant),
			NewIdempotencyInterceptor(store, cfg),
		),
	)
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	client := paladiniamv1connect.NewUserSettingsServiceClient(srv.Client(), srv.URL)
	return client, svc, srv.Close
}

// TestMemoizeRoundTrip: with a header set, two identical calls hit
// the handler exactly once. The second call's response is
// reconstructed from the cached bytes. We assert both the call
// count AND payload equality, so a silent fall-through bug (handler
// runs twice and we still see equal payloads) would be caught by
// the call-count check.
func TestMemoizeRoundTrip(t *testing.T) {
	store := newMemStore()
	client, svc, cleanup := newSystemServer(t, store, IdempotencyConfig{TTL: time.Minute})
	defer cleanup()

	key := uuid.NewString()
	req := connect.NewRequest(&iamv1.UpdateMineRequest{})
	req.Header().Set("Idempotency-Key", key)

	first, err := client.UpdateMine(context.Background(), req)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}

	req2 := connect.NewRequest(&iamv1.UpdateMineRequest{})
	req2.Header().Set("Idempotency-Key", key)
	second, err := client.UpdateMine(context.Background(), req2)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}

	if svc.calls != 1 {
		t.Fatalf("handler must run exactly once on replay; got %d calls", svc.calls)
	}
	if first.Msg.Name != second.Msg.Name || first.Msg.Timezone != second.Msg.Timezone {
		t.Fatalf("replay payload mismatch: %+v vs %+v", first.Msg, second.Msg)
	}
}

// TestNoMemoizeWithoutHeader: without a header the interceptor is a
// pass-through. Two calls hit the handler twice, no Put happens.
func TestNoMemoizeWithoutHeader(t *testing.T) {
	store := newMemStore()
	client, svc, cleanup := newSystemServer(t, store, IdempotencyConfig{TTL: time.Minute})
	defer cleanup()

	for range 2 {
		if _, err := client.UpdateMine(context.Background(),
			connect.NewRequest(&iamv1.UpdateMineRequest{})); err != nil {
			t.Fatalf("call: %v", err)
		}
	}
	if svc.calls != 2 {
		t.Fatalf("handler must run on every call without header; got %d", svc.calls)
	}
	if len(store.data) != 0 {
		t.Fatalf("store must stay empty without header; got %d entries", len(store.data))
	}
}

// TestNoMemoizeOnError: a failed handler must NOT memoize — the
// caller's retry must reach the handler. We force a one-shot failure
// then succeed on retry; both calls must hit the handler.
func TestNoMemoizeOnError(t *testing.T) {
	store := newMemStore()
	client, svc, cleanup := newSystemServer(t, store, IdempotencyConfig{TTL: time.Minute})
	defer cleanup()
	svc.failOnce = true

	key := uuid.NewString()
	req := connect.NewRequest(&iamv1.UpdateMineRequest{})
	req.Header().Set("Idempotency-Key", key)

	// First call: handler returns Unavailable — must NOT cache.
	if _, err := client.UpdateMine(context.Background(), req); err == nil {
		t.Fatal("expected first call to fail")
	}

	// Second call with same key: must reach handler (retry succeeds).
	req2 := connect.NewRequest(&iamv1.UpdateMineRequest{})
	req2.Header().Set("Idempotency-Key", key)
	if _, err := client.UpdateMine(context.Background(), req2); err != nil {
		t.Fatalf("second call: %v", err)
	}
	if svc.calls != 2 {
		t.Fatalf("handler must run on retry after error; got %d calls", svc.calls)
	}
}

// A procedure in SkipMethods must not be memoized even when the caller sends a
// key — the guard that makes it safe for clients to stamp mutations broadly.
//
// The hazard is RefreshToken. Refresh tokens rotate: presenting one
// invalidates it, and presenting a rotated one again is treated as theft
// (RFC 6819), which revokes the whole family. Replaying a cached response
// would hand a second caller the already-rotated pair. Nothing reached this
// before, because no client sent a key on an auth RPC; that stopped being true
// when the console widened its stamping, and "unreachable" was never the same
// as "guarded".
func TestSkipMethodsIsNotMemoized(t *testing.T) {
	store := newMemStore()
	client, svc, cleanup := newSystemServer(t, store, IdempotencyConfig{
		TTL: time.Minute,
		SkipMethods: map[string]bool{
			paladiniamv1connect.UserSettingsServiceUpdateMineProcedure: true,
		},
	})
	defer cleanup()

	key := uuid.NewString()
	for i := 0; i < 2; i++ {
		req := connect.NewRequest(&iamv1.UpdateMineRequest{})
		req.Header().Set("Idempotency-Key", key)
		if _, err := client.UpdateMine(context.Background(), req); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}

	if svc.calls != 2 {
		t.Fatalf("a skipped procedure was memoized: handler ran %d times, want 2", svc.calls)
	}
}

// The list the production wiring passes must actually name the credential
// minters. Asserting the wiring rather than the mechanism: a correct skip
// implementation pointed at an empty map protects nothing, which is what the
// config did until this landed.
func TestCredentialMintersAreSkipped(t *testing.T) {
	for _, proc := range []string{
		"/paladin.iam.v1.AuthService/Login",
		"/paladin.iam.v1.AuthService/RefreshToken",
		"/paladin.iam.v1.AuthService/ExchangeAudience",
		"/paladin.iam.v1.AuthService/SwitchTenant",
	} {
		if !CredentialMintingProcedures[proc] {
			t.Errorf("%s is not in CredentialMintingProcedures — a memoized "+
				"credential is replayable", proc)
		}
	}
	// Capability issuance is deliberately absent: a capability is minted
	// against a scope the caller names, so collapsing a double-submit onto one
	// capability is what the key is FOR. Its token is not cached; see
	// TestCredentialResponsesAreStoredRedacted.
	if CredentialMintingProcedures["/paladin.admin.v1.CapabilityService/Issue"] {
		t.Error("CapabilityService/Issue must stay memoizable")
	}
}

// A key used for one request and then sent with a different one used to get
// the first request's response back: the cache was keyed (tenant, method,
// key) and nothing compared the requests. A download helper reusing one key
// handed back the first object's URL under every name. It must be refused,
// and the handler must not run for it.
func TestKeyReusedWithADifferentRequestIsRefused(t *testing.T) {
	store := newMemStore()
	client, svc, cleanup := newSystemServer(t, store, IdempotencyConfig{TTL: time.Minute})
	defer cleanup()

	key := uuid.NewString()
	first := connect.NewRequest(&iamv1.UpdateMineRequest{Timezone: "Europe/Kyiv"})
	first.Header().Set("Idempotency-Key", key)
	if _, err := client.UpdateMine(context.Background(), first); err != nil {
		t.Fatalf("first call: %v", err)
	}

	other := connect.NewRequest(&iamv1.UpdateMineRequest{Timezone: "Europe/Warsaw"})
	other.Header().Set("Idempotency-Key", key)
	_, err := client.UpdateMine(context.Background(), other)
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument for a key reused with another request", connect.CodeOf(err))
	}
	if svc.calls != 1 {
		t.Errorf("handler ran %d times, want 1 — the refused call must not reach it", svc.calls)
	}

	// The same request with the same key still replays.
	again := connect.NewRequest(&iamv1.UpdateMineRequest{Timezone: "Europe/Kyiv"})
	again.Header().Set("Idempotency-Key", key)
	if _, err := client.UpdateMine(context.Background(), again); err != nil {
		t.Fatalf("replay of the same request: %v", err)
	}
	if svc.calls != 1 {
		t.Errorf("handler ran %d times after a same-request replay, want 1", svc.calls)
	}
}

// A row written before fingerprints were stored carries none, and replays for
// any request with its key, as every row did before — refusing it would turn
// a deploy into a burst of InvalidArgument for keys already in flight.
func TestRecordWithoutFingerprintStillReplays(t *testing.T) {
	store := newMemStore()
	client, svc, cleanup := newSystemServer(t, store, IdempotencyConfig{TTL: time.Minute})
	defer cleanup()

	key := uuid.NewString()
	seed := connect.NewRequest(&iamv1.UpdateMineRequest{Timezone: "Europe/Kyiv"})
	seed.Header().Set("Idempotency-Key", key)
	if _, err := client.UpdateMine(context.Background(), seed); err != nil {
		t.Fatalf("seed call: %v", err)
	}
	store.mu.Lock()
	for k, rec := range store.data {
		rec.RequestHash = nil
		store.data[k] = rec
	}
	store.mu.Unlock()

	other := connect.NewRequest(&iamv1.UpdateMineRequest{Timezone: "Europe/Warsaw"})
	other.Header().Set("Idempotency-Key", key)
	if _, err := client.UpdateMine(context.Background(), other); err != nil {
		t.Fatalf("legacy row must replay, got %v", err)
	}
	if svc.calls != 1 {
		t.Errorf("handler ran %d times, want 1 (replayed)", svc.calls)
	}
}
