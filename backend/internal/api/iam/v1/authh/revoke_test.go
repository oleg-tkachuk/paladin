package authh

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"
)

// AuthService.Revoke was at 0%, and it is the endpoint a user reaches for when
// they believe a session is compromised. Two of its properties are easy to
// regress and invisible from outside.

type revokeRecordingRefresh struct {
	fakeRefresh
	revoked []uuid.UUID
}

func (f *revokeRecordingRefresh) Revoke(_ context.Context, jti uuid.UUID) error {
	f.revoked = append(f.revoked, jti)
	return nil
}

func TestRevokeRevokesTheRefreshTokenByJTI(t *testing.T) {
	jti := uuid.New()
	refresh := &revokeRecordingRefresh{}
	h := NewHandler(&fakeUsers{}, refresh, &stubMinter{},
		stubDecoder{jti: jti, uid: uuid.New(), tid: uuid.New()}, allowAuthorizer{})

	if err := h.Revoke(context.Background(), "a-refresh-token"); err != nil {
		t.Fatalf("Revoke: %v", err)
	}
	if len(refresh.revoked) != 1 || refresh.revoked[0] != jti {
		t.Fatalf("revoked %v, want exactly the presented token's jti %v", refresh.revoked, jti)
	}
}

func TestRevokeRequiresAToken(t *testing.T) {
	h := newHandler(&fakeUsers{}, &revokeRecordingRefresh{}, &stubMinter{})

	if err := h.Revoke(context.Background(), ""); code(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument", code(err))
	}
}

// An access token cannot be revoked — they are stateless and there is no
// denylist. The handler answers OK anyway, deliberately, so a client that
// revokes whatever it holds is idempotent rather than being taught to retry.
//
// This is the test that says the silence is a decision. Without it, a future
// reader sees "returns nil and does nothing" and cannot tell it from a bug —
// and the day a denylist arrives, this test is the one that has to change,
// which is exactly the right place for the conversation to happen.
func TestRevokeOfANonRefreshTokenIsAcceptedAndDoesNothing(t *testing.T) {
	refresh := &revokeRecordingRefresh{}
	h := NewHandler(&fakeUsers{}, refresh, &stubMinter{},
		stubDecoder{err: errors.New("not a refresh token")}, allowAuthorizer{})

	if err := h.Revoke(context.Background(), "an-access-token"); err != nil {
		t.Fatalf("Revoke of an access token = %v, want nil (idempotent by design)", err)
	}
	if len(refresh.revoked) != 0 {
		t.Errorf("something was revoked from an unparseable token: %v", refresh.revoked)
	}
}
