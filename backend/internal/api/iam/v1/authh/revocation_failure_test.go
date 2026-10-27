package authh

import (
	"context"
	"errors"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
)

var errRevokeFailed = errors.New("pool closed")

// failingRevocation is a store whose revocations fail.
type failingRevocation struct{ fakeRefresh }

func (f *failingRevocation) RevokeFamilyOf(context.Context, uuid.UUID) (int64, error) {
	return 0, errRevokeFailed
}

func (f *failingRevocation) RevokeForUser(context.Context, uuid.UUID) (int64, error) {
	return 0, errRevokeFailed
}

// A logout whose session could not be ended says so: answering OK left the
// session live while the user believed it gone.
func TestRevokeReportsASessionItCouldNotEnd(t *testing.T) {
	h := NewHandler(&fakeUsers{}, &failingRevocation{}, &stubMinter{},
		stubDecoder{jti: uuid.New(), uid: uuid.New(), tid: uuid.New()}, allowAuthorizer{})
	err := h.Revoke(context.Background(), "a-refresh-token")
	if connect.CodeOf(err) != connect.CodeInternal || !errors.Is(err, errRevokeFailed) {
		t.Fatalf("Revoke = %v, want Internal wrapping the store's error", err)
	}
}

// A changed password stands when the old sessions could not be ended, and
// the failure is logged: those sessions are still live.
func TestChangePasswordLogsSessionsItCouldNotEnd(t *testing.T) {
	u := userWithPassword(t, "old")
	h := newHandler(&fakeUsers{user: u}, &failingRevocation{}, &stubMinter{})
	core, logs := observer.New(zap.ErrorLevel)
	ctx := logger.WithContext(ctxAsUser(u.UserID), zap.New(core))

	if err := h.ChangePassword(ctx, "old", "brand-new"); err != nil {
		t.Fatalf("ChangePassword: %v", err)
	}
	got := logs.FilterMessage("sessions not ended after a password change").All()
	if len(got) != 1 || got[0].ContextMap()["user_id"] != u.UserID.String() {
		t.Errorf("logged %v, want one line naming the user", got)
	}
}
