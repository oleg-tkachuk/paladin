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

	_ "github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1"
	_ "github.com/oleg-tkachuk/paladin/internal/api/pb/data/v1"
	_ "github.com/oleg-tkachuk/paladin/internal/api/pb/iam/v1"
)

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
// a sibling RPC on the same service that is NOT a read still replays.
//
// It was TestUndeclaredSiblingStillMemoizes until UpdateMine was declared
// IDEMPOTENT, at which point the name described a case this service no longer
// has — UserSettingsService has no undeclared method left. The assertion is
// better for it: IDEMPOTENT is exactly the level whose responses SHOULD be
// replayed, since an idempotent write still writes and the key exists to
// collapse the retry.
func TestIdempotentSiblingStillMemoizes(t *testing.T) {
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
