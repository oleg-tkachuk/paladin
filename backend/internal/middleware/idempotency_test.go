package middleware

import (
	"context"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary/unarytest"
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

// TestReplayResponseDecodesThroughTheSchema exercises the replay
// round-trip: marshal a VersionInfo, throw the live message away, then
// rebuild a fresh one from just the bytes and the method's schema. The
// replayed value must
//   - be the method's output type (the interceptor returns it as-is, and
//     the Connect serializer would blow up at write-time on the wrong type),
//   - carry the same proto fields.
//
// The schema is the only registry replay needs: the method descriptor names
// its output type, and the global type registry builds it. There is nothing
// to register per method, so no method can be missing from a registry.
func TestReplayResponseDecodesThroughTheSchema(t *testing.T) {
	original := &iamv1.VersionInfo{
		Version:   "1.2.3",
		Commit:    "abc1234",
		GoVersion: "go1.26.0",
	}
	body, err := proto.Marshal(original)
	if err != nil {
		t.Fatalf("proto.Marshal: %v", err)
	}

	replayed, err := replayResponse(probeSpec(), body)
	if err != nil {
		t.Fatalf("replayResponse: %v", err)
	}
	got, ok := replayed.(*iamv1.VersionInfo)
	if !ok {
		t.Fatalf("replayed %T, want *iamv1.VersionInfo", replayed)
	}
	if !proto.Equal(got, original) {
		t.Fatalf("replayed payload mismatch: %+v", got)
	}
}

// A method with no protobuf schema cannot be replayed. The interceptor treats
// that as an unreplayable row and runs the handler, so it must be an error
// rather than an empty message passed off as the cached answer.
func TestReplayResponseNeedsASchema(t *testing.T) {
	if _, err := replayResponse(connect.Spec{Procedure: unarytest.ProbeProcedure}, nil); err == nil {
		t.Fatal("replayed a response for a method with no schema")
	}
}

// probeSpec is the Spec of unarytest.Probe's procedure, schema included.
func probeSpec() connect.Spec {
	return connect.Spec{
		StreamType: connect.StreamTypeUnary,
		Procedure:  unarytest.ProbeProcedure,
		Schema: iamv1.File_paladin_iam_v1_health_service_proto.Services().
			ByName("HealthService").Methods().ByName("GetVersion"),
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

func (s *stubSystem) UpdateMine(_ context.Context, _ *iamv1.UpdateMineRequest) (*iamv1.UserSettings, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls++
	if s.failOnce {
		s.failOnce = false
		return nil, connect.NewError(connect.CodeUnavailable, "transient")
	}
	return &iamv1.UserSettings{
		Name:     "users/me/settings",
		Timezone: "Europe/Kyiv",
	}, nil
}

// principalCtx is a context carrying a fixed Principal. The idempotency
// interceptor needs a tenant; in production that comes from the
// auth.Interceptor upstream. For the unit-test plane we inline a minimal
// stand-in: the in-process transport serves the handler on the caller's
// context, so a principal on it reaches every interceptor.
func principalCtx(tenantID uuid.UUID) context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{
		TenantID: tenantID,
		Subject:  testSubject,
	})
}

// testSubject is the principal the unit-test plane calls as.
const testSubject = "test"

// withIdempotencyKey is ctx for a call that sends key as its Idempotency-Key.
func withIdempotencyKey(ctx context.Context, key string) context.Context {
	return unarytest.WithHeader(ctx, idempotencyHeader, key)
}

// newSystemServer serves stubSystem behind the idempotency interceptor and
// returns its client, the stub, and the context — one tenant's principal —
// to call it under.
func newSystemServer(t *testing.T, store IdempotencyStore, cfg IdempotencyConfig) (paladiniamv1connect.UserSettingsServiceClient, *stubSystem, context.Context) {
	t.Helper()
	svc := &stubSystem{}
	client := paladiniamv1connect.NewUserSettingsServiceClient(unarytest.Client(func(s *connect.Server) {
		paladiniamv1connect.RegisterUserSettingsServiceHandler(s, svc)
	}, NewIdempotencyInterceptor(store, cfg)))
	return client, svc, principalCtx(uuid.New())
}

// TestMemoizeRoundTrip: with a header set, two identical calls hit
// the handler exactly once. The second call's response is
// reconstructed from the cached bytes. We assert both the call
// count AND payload equality, so a silent fall-through bug (handler
// runs twice and we still see equal payloads) would be caught by
// the call-count check.
func TestMemoizeRoundTrip(t *testing.T) {
	store := newMemStore()
	client, svc, ctx := newSystemServer(t, store, IdempotencyConfig{TTL: time.Minute})

	key := uuid.NewString()
	req := &iamv1.UpdateMineRequest{}

	first, err := client.UpdateMine(withIdempotencyKey(ctx, key), req)
	if err != nil {
		t.Fatalf("first call: %v", err)
	}

	req2 := &iamv1.UpdateMineRequest{}
	second, err := client.UpdateMine(withIdempotencyKey(ctx, key), req2)
	if err != nil {
		t.Fatalf("second call: %v", err)
	}

	if svc.calls != 1 {
		t.Fatalf("handler must run exactly once on replay; got %d calls", svc.calls)
	}
	if first.Name != second.Name || first.Timezone != second.Timezone {
		t.Fatalf("replay payload mismatch: %+v vs %+v", first, second)
	}
}

// TestNoMemoizeWithoutHeader: without a header the interceptor is a
// pass-through. Two calls hit the handler twice, no Put happens.
func TestNoMemoizeWithoutHeader(t *testing.T) {
	store := newMemStore()
	client, svc, ctx := newSystemServer(t, store, IdempotencyConfig{TTL: time.Minute})

	for range 2 {
		if _, err := client.UpdateMine(ctx,
			&iamv1.UpdateMineRequest{}); err != nil {
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
	client, svc, ctx := newSystemServer(t, store, IdempotencyConfig{TTL: time.Minute})
	svc.failOnce = true

	key := uuid.NewString()
	req := &iamv1.UpdateMineRequest{}

	// First call: handler returns Unavailable — must NOT cache.
	if _, err := client.UpdateMine(withIdempotencyKey(ctx, key), req); err == nil {
		t.Fatal("expected first call to fail")
	}

	// Second call with same key: must reach handler (retry succeeds).
	req2 := &iamv1.UpdateMineRequest{}
	if _, err := client.UpdateMine(withIdempotencyKey(ctx, key), req2); err != nil {
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
	client, svc, ctx := newSystemServer(t, store, IdempotencyConfig{
		TTL: time.Minute,
		SkipMethods: map[string]bool{
			paladiniamv1connect.UserSettingsServiceUpdateMineProcedure: true,
		},
	})

	key := uuid.NewString()
	for i := 0; i < 2; i++ {
		req := &iamv1.UpdateMineRequest{}
		if _, err := client.UpdateMine(withIdempotencyKey(ctx, key), req); err != nil {
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
	client, svc, ctx := newSystemServer(t, store, IdempotencyConfig{TTL: time.Minute})

	key := uuid.NewString()
	first := &iamv1.UpdateMineRequest{Timezone: "Europe/Kyiv"}
	if _, err := client.UpdateMine(withIdempotencyKey(ctx, key), first); err != nil {
		t.Fatalf("first call: %v", err)
	}

	other := &iamv1.UpdateMineRequest{Timezone: "Europe/Warsaw"}
	_, err := client.UpdateMine(withIdempotencyKey(ctx, key), other)
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument for a key reused with another request", connect.CodeOf(err))
	}
	if svc.calls != 1 {
		t.Errorf("handler ran %d times, want 1 — the refused call must not reach it", svc.calls)
	}

	// The same request with the same key still replays.
	again := &iamv1.UpdateMineRequest{Timezone: "Europe/Kyiv"}
	if _, err := client.UpdateMine(withIdempotencyKey(ctx, key), again); err != nil {
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
	client, svc, ctx := newSystemServer(t, store, IdempotencyConfig{TTL: time.Minute})

	key := uuid.NewString()
	seed := &iamv1.UpdateMineRequest{Timezone: "Europe/Kyiv"}
	if _, err := client.UpdateMine(withIdempotencyKey(ctx, key), seed); err != nil {
		t.Fatalf("seed call: %v", err)
	}
	store.mu.Lock()
	for k, rec := range store.data {
		rec.RequestHash = nil
		store.data[k] = rec
	}
	store.mu.Unlock()

	other := &iamv1.UpdateMineRequest{Timezone: "Europe/Warsaw"}
	if _, err := client.UpdateMine(withIdempotencyKey(ctx, key), other); err != nil {
		t.Fatalf("legacy row must replay, got %v", err)
	}
	if svc.calls != 1 {
		t.Errorf("handler ran %d times, want 1 (replayed)", svc.calls)
	}
}
