package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	iamv1 "github.com/oleg-tkachuk/paladin/internal/api/pb/iam/v1"
	"github.com/oleg-tkachuk/paladin/internal/api/pb/iam/v1/paladiniamv1connect"

	"google.golang.org/protobuf/types/descriptorpb"

	_ "github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1"
	_ "github.com/oleg-tkachuk/paladin/internal/api/pb/data/v1"
	_ "github.com/oleg-tkachuk/paladin/internal/api/pb/iam/v1"
)

// Resolved from the real descriptors, not from a hand-written table: the point
// of the change is that the proto is the source, so a test asserting against a
// second copy of the answer would prove nothing about it.
func TestMethodIdempotencyReadsTheDescriptor(t *testing.T) {
	cases := []struct {
		procedure string
		want      descriptorpb.MethodOptions_IdempotencyLevel
		why       string
	}{
		{
			"/paladin.admin.v1.BucketService/GetBucket",
			descriptorpb.MethodOptions_NO_SIDE_EFFECTS,
			"annotated in api/v0.6.0 after its handler was read",
		},
		{
			"/paladin.data.v1.ObjectService/UploadObject",
			descriptorpb.MethodOptions_IDEMPOTENCY_UNKNOWN,
			"creates an object; the honest default, and the RPC the old name-prefix rule missed",
		},
		{
			// DownloadObject rather than HealthService/GetVersion, which this
			// case used until GetVersion was annotated and broke it. An exemplar
			// picked for being unannotated has a shelf life; this one is picked
			// for being unannotABLE. It reads like a read and is not one — it
			// records the presign it issues and charges the tenant's quota — so
			// it can never carry NO_SIDE_EFFECTS however far the work proceeds.
			"/paladin.data.v1.ObjectService/DownloadObject",
			descriptorpb.MethodOptions_IDEMPOTENCY_UNKNOWN,
			"named like a read, but records a presign and charges quota — unverified " +
				"must not be mistaken for declared",
		},
		{
			"/paladin.admin.v1.NoSuchService/NoSuchMethod",
			descriptorpb.MethodOptions_IDEMPOTENCY_UNKNOWN,
			"unknown procedure falls back to claiming nothing",
		},
		{
			"malformed-not-a-procedure",
			descriptorpb.MethodOptions_IDEMPOTENCY_UNKNOWN,
			"a string that is not a procedure must not panic or guess",
		},
	}
	for _, c := range cases {
		if got := methodIdempotency(c.procedure); got != c.want {
			t.Errorf("%s = %v, want %v (%s)", c.procedure, got, c.want, c.why)
		}
	}
}

// The distinction that decides whether a response may be replayed.
//
// IDEMPOTENT means repeating the call is safe — an idempotent write still
// writes, and replaying its response is legitimate. NO_SIDE_EFFECTS means the
// response is a snapshot, and handing back an old one answers a question the
// caller did not ask.
func TestOnlyNoSideEffectsCountsAsARead(t *testing.T) {
	if !declaredRead("/paladin.admin.v1.BucketService/GetBucket") {
		t.Error("an annotated read is not recognised — declared reads would still be memoized")
	}
	if declaredRead("/paladin.data.v1.ObjectService/UploadObject") {
		t.Error("UploadObject treated as a read; its replay is the whole point of the key")
	}
	if declaredRead("/paladin.data.v1.ObjectService/DownloadObject") {
		t.Error("an UNANNOTATED procedure must not be treated as declared — that " +
			"would extend the guarantee to RPCs nobody checked")
	}
}

// The cache must not turn one lookup into the answer for every procedure.
func TestIdempotencyLookupCachesPerProcedure(t *testing.T) {
	a := methodIdempotency("/paladin.admin.v1.BucketService/GetBucket")
	b := methodIdempotency("/paladin.data.v1.ObjectService/UploadObject")
	if a == b {
		t.Fatalf("both procedures resolved to %v — the cache is keyed wrongly", a)
	}
	if got := methodIdempotency("/paladin.admin.v1.BucketService/GetBucket"); got != a {
		t.Errorf("second lookup returned %v, first returned %v", got, a)
	}
}

// ─── behaviour, not just the predicate ──────────────────────────────────────

// stubSettings implements UserSettingsService. GetMine is annotated
// NO_SIDE_EFFECTS; UpdateMine is not. Both count their calls, which is how the
// test tells a replay from a real invocation.
type stubSettings struct {
	getCalls    int
	updateCalls int
}

func (s *stubSettings) GetMine(_ context.Context, _ *connect.Request[iamv1.GetMineRequest]) (*connect.Response[iamv1.UserSettings], error) {
	s.getCalls++
	return connect.NewResponse(&iamv1.UserSettings{Theme: "dark"}), nil
}

func (s *stubSettings) UpdateMine(_ context.Context, _ *connect.Request[iamv1.UpdateMineRequest]) (*connect.Response[iamv1.UserSettings], error) {
	s.updateCalls++
	return connect.NewResponse(&iamv1.UserSettings{Theme: "light"}), nil
}

func (s *stubSettings) GetForUser(_ context.Context, _ *connect.Request[iamv1.GetForUserRequest]) (*connect.Response[iamv1.UserSettings], error) {
	return connect.NewResponse(&iamv1.UserSettings{}), nil
}

func (s *stubSettings) ListByTenant(_ context.Context, _ *connect.Request[iamv1.ListByTenantRequest]) (*connect.Response[iamv1.ListByTenantResponse], error) {
	return connect.NewResponse(&iamv1.ListByTenantResponse{}), nil
}

func (s *stubSettings) DeleteForUser(_ context.Context, _ *connect.Request[iamv1.DeleteForUserRequest]) (*connect.Response[iamv1.DeleteForUserResponse], error) {
	return connect.NewResponse(&iamv1.DeleteForUserResponse{}), nil
}

func newSettingsServer(t *testing.T, store IdempotencyStore) (paladiniamv1connect.UserSettingsServiceClient, *stubSettings, func()) {
	t.Helper()
	svc := &stubSettings{}
	mux := http.NewServeMux()
	path, handler := paladiniamv1connect.NewUserSettingsServiceHandler(svc,
		connect.WithInterceptors(
			principalInjector(uuid.New()),
			NewIdempotencyInterceptor(store, IdempotencyConfig{TTL: time.Minute}),
		),
	)
	mux.Handle(path, handler)
	srv := httptest.NewServer(mux)
	return paladiniamv1connect.NewUserSettingsServiceClient(srv.Client(), srv.URL), svc, srv.Close
}

// A declared read runs every time, key or no key. Replaying it would hand the
// caller a snapshot from up to a TTL ago in answer to a question about now —
// and before the descriptors carried the claim, a client that sent a key on a
// read got exactly that.
func TestDeclaredReadIsNeverMemoized(t *testing.T) {
	client, svc, cleanup := newSettingsServer(t, newMemStore())
	defer cleanup()

	key := uuid.NewString()
	for i := 0; i < 2; i++ {
		req := connect.NewRequest(&iamv1.GetMineRequest{})
		req.Header().Set("Idempotency-Key", key)
		if _, err := client.GetMine(context.Background(), req); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if svc.getCalls != 2 {
		t.Errorf("GetMine ran %d times, want 2 — a declared read was replayed", svc.getCalls)
	}
}

// The other half, and the reason this is not just "stop memoizing everything":
// a sibling RPC on the same service, not declared a read, still replays.
func TestUndeclaredSiblingStillMemoizes(t *testing.T) {
	client, svc, cleanup := newSettingsServer(t, newMemStore())
	defer cleanup()

	key := uuid.NewString()
	for i := 0; i < 2; i++ {
		req := connect.NewRequest(&iamv1.UpdateMineRequest{})
		req.Header().Set("Idempotency-Key", key)
		if _, err := client.UpdateMine(context.Background(), req); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if svc.updateCalls != 1 {
		t.Errorf("UpdateMine ran %d times, want 1 — memoization stopped working", svc.updateCalls)
	}
}

// All three levels, decided without going through the descriptors.
//
// Necessary because only NO_SIDE_EFFECTS appears in the tree today: a test that
// resolved real procedures could not distinguish this rule from one that also
// treated IDEMPOTENT as a read, and a mutation doing exactly that passed the
// whole suite until this existed.
func TestOnlyNoSideEffectsForbidsMemoize(t *testing.T) {
	cases := []struct {
		level  descriptorpb.MethodOptions_IdempotencyLevel
		forbid bool
		why    string
	}{
		{descriptorpb.MethodOptions_NO_SIDE_EFFECTS, true,
			"a read's response is a snapshot; replaying it answers the wrong question"},
		{descriptorpb.MethodOptions_IDEMPOTENT, false,
			"repeating is safe, but it still writes — replaying its response is the point"},
		{descriptorpb.MethodOptions_IDEMPOTENCY_UNKNOWN, false,
			"claims nothing, so nothing is forbidden"},
	}
	for _, c := range cases {
		if got := levelForbidsMemoize(c.level); got != c.forbid {
			t.Errorf("%v forbids memoize = %v, want %v (%s)", c.level, got, c.forbid, c.why)
		}
	}
}
