package middleware

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary/unarytest"

	iamv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1/paladiniamv1connect"

	_ "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	_ "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	_ "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
)

// ─── behaviour, not just the predicate ──────────────────────────────────────

// stubSettings implements UserSettingsService. GetMine is annotated
// NO_SIDE_EFFECTS; UpdateMine is not. Both count their calls, which is how the
// test tells a replay from a real invocation.
type stubSettings struct {
	getCalls    int
	updateCalls int
}

func (s *stubSettings) GetMine(_ context.Context, _ *iamv1.GetMineRequest) (*iamv1.UserSettings, error) {
	s.getCalls++
	return &iamv1.UserSettings{Theme: "dark"}, nil
}

func (s *stubSettings) UpdateMine(_ context.Context, _ *iamv1.UpdateMineRequest) (*iamv1.UserSettings, error) {
	s.updateCalls++
	return &iamv1.UserSettings{Theme: "light"}, nil
}

func (s *stubSettings) GetForUser(_ context.Context, _ *iamv1.GetForUserRequest) (*iamv1.UserSettings, error) {
	return &iamv1.UserSettings{}, nil
}

func (s *stubSettings) ListByTenant(_ context.Context, _ *iamv1.ListByTenantRequest) (*iamv1.ListByTenantResponse, error) {
	return &iamv1.ListByTenantResponse{}, nil
}

func (s *stubSettings) DeleteForUser(_ context.Context, _ *iamv1.DeleteForUserRequest) (*iamv1.DeleteForUserResponse, error) {
	return &iamv1.DeleteForUserResponse{}, nil
}

func newSettingsServer(t *testing.T, store IdempotencyStore) (paladiniamv1connect.UserSettingsServiceClient, *stubSettings, context.Context) {
	t.Helper()
	svc := &stubSettings{}
	client := paladiniamv1connect.NewUserSettingsServiceClient(unarytest.Client(func(s *connect.Server) {
		paladiniamv1connect.RegisterUserSettingsServiceHandler(s, svc)
	}, NewIdempotencyInterceptor(store, IdempotencyConfig{TTL: time.Minute})))
	return client, svc, principalCtx(uuid.New())
}

// A declared read runs every time, key or no key. Replaying it would hand the
// caller a snapshot from up to a TTL ago in answer to a question about now —
// and before the descriptors carried the claim, a client that sent a key on a
// read got exactly that.
func TestDeclaredReadIsNeverMemoized(t *testing.T) {
	client, svc, ctx := newSettingsServer(t, newMemStore())

	key := uuid.NewString()
	for i := 0; i < 2; i++ {
		req := &iamv1.GetMineRequest{}
		if _, err := client.GetMine(withIdempotencyKey(ctx, key), req); err != nil {
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
	client, svc, ctx := newSettingsServer(t, newMemStore())

	key := uuid.NewString()
	for i := 0; i < 2; i++ {
		req := &iamv1.UpdateMineRequest{}
		if _, err := client.UpdateMine(withIdempotencyKey(ctx, key), req); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if svc.updateCalls != 1 {
		t.Errorf("UpdateMine ran %d times, want 1 — memoization stopped working", svc.updateCalls)
	}
}

// ─── the skip that must not depend on being wired ───────────────────────────

// stubAuth counts RefreshToken calls. Everything else is Unimplemented.
type stubAuth struct {
	paladiniamv1connect.UnimplementedAuthServiceHandler
	calls int
}

func (s *stubAuth) RefreshToken(_ context.Context, _ *iamv1.RefreshTokenRequest) (*iamv1.RefreshTokenResponse, error) {
	s.calls++
	return &iamv1.RefreshTokenResponse{
		Tokens: &iamv1.TokenPair{AccessToken: "a"},
	}, nil
}

// An interceptor built with an EMPTY config still refuses to memoize a
// credential minter.
//
// The config here is deliberately the one the ADMIN listener passes — no
// SkipMethods at all — because that is what a dropped wiring line looks like.
// Before the constructor merged the list in, this test failed: RefreshToken ran
// once and the second call got the first response back, which is a refresh pair
// the server had already rotated away and, presented again, is what RFC 6819
// theft detection revokes an entire family over.
//
// It became reachable when the clients moved onto idempotency_level. Login and
// RefreshToken declare IDEMPOTENCY_UNKNOWN, so every client stamps them now;
// previously none did, and the hazard sat behind a door nobody opened.
func TestCredentialMinterIsSkippedWithoutBeingConfigured(t *testing.T) {
	svc := &stubAuth{}
	client := paladiniamv1connect.NewAuthServiceClient(unarytest.Client(func(s *connect.Server) {
		paladiniamv1connect.RegisterAuthServiceHandler(s, svc)
	},
		// No SkipMethods. The point of the test.
		NewIdempotencyInterceptor(newMemStore(), IdempotencyConfig{TTL: time.Minute}),
	))
	ctx := principalCtx(uuid.New())

	key := uuid.NewString()
	for i := 0; i < 2; i++ {
		req := &iamv1.RefreshTokenRequest{RefreshToken: "r"}
		if _, err := client.RefreshToken(withIdempotencyKey(ctx, key), req); err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	if svc.calls != 2 {
		t.Errorf("RefreshToken ran %d times, want 2 — a credential minter was "+
			"memoized because nobody passed the skip list", svc.calls)
	}
}
