package eventsubh

import (
	"errors"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
)

var errStoreDown = errors.New("connection reset")

// A listing the store fails used to reach the caller with no code at all
// (UNKNOWN) and no line saying why.
func TestListAnswersAFailingStoreAsInternal(t *testing.T) {
	tid := uuid.New()
	h, logs := loggedHandler(&fakeRepo{listErr: errStoreDown})
	if _, _, err := h.List(ctxAs(tid, apiutil.RoleTenantAdmin), admindomain.ListEventSubscriptionsArgs{}); code(err) != connect.CodeInternal {
		t.Fatalf("code = %v, want Internal", code(err))
	}
	if logs.FilterLevelExact(zapcore.ErrorLevel).FilterField(zap.String("tenant_id", tid.String())).Len() != 1 {
		t.Errorf("want one error line naming the tenant, got %v", logs.All())
	}
}

// A read is logged by what it ended as: a failing store at error, a
// subscription that is not there at warn.
func TestGetLogsTheStoreAnswer(t *testing.T) {
	for _, tc := range []struct {
		name  string
		err   error
		code  connect.Code
		level zapcore.Level
	}{
		{"store failing", errStoreDown, connect.CodeInternal, zapcore.ErrorLevel},
		{"not there", admindomain.ErrNotFound, connect.CodeNotFound, zapcore.WarnLevel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tid, id := uuid.New(), uuid.New()
			h, logs := loggedHandler(&fakeRepo{getErr: tc.err})
			if _, err := h.Get(ctxAs(tid, apiutil.RoleTenantAdmin), tid, id); code(err) != tc.code {
				t.Fatalf("code = %v, want %v", code(err), tc.code)
			}
			if logs.FilterLevelExact(tc.level).FilterField(zap.String("subscription_id", id.String())).Len() != 1 {
				t.Errorf("want one %v line naming the subscription, got %v", tc.level, logs.All())
			}
		})
	}
}

// Every server fault is an error line, not only INTERNAL: an unavailable
// store is as much the server's as a broken one.
func TestAWriteTheServerFailsIsAnErrorLine(t *testing.T) {
	tid := uuid.New()
	repo := &fakeRepo{
		sub:       admindomain.EventSubscription{TenantID: tid, SubscriptionID: uuid.New(), SinkKind: "http"},
		deleteErr: connect.NewError(connect.CodeUnavailable, "store unavailable"),
	}
	h, logs := loggedHandler(repo)
	if err := h.Delete(ctxAs(tid, apiutil.RolePlatformAdmin), tid, repo.sub.SubscriptionID, 0); code(err) != connect.CodeUnavailable {
		t.Fatalf("code = %v, want Unavailable", code(err))
	}
	if logs.FilterLevelExact(zapcore.ErrorLevel).Len() != 1 {
		t.Errorf("want one error line, got %v", logs.All())
	}
}
