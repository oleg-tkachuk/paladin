package eventsubh

import (
	"errors"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
)

type offKinds struct{}

func (offKinds) Enabled(string) bool { return false }

func loggedHandler(repo *fakeRepo) (*Handler, *observer.ObservedLogs) {
	core, logs := observer.New(zapcore.DebugLevel)
	h := NewHandler(repo, allowAuthorizer{})
	h.SetLogger(zap.New(core))
	return h, logs
}

// An update that wrote but could not read its row back used to answer with
// an empty subscription as if it had succeeded. It is an internal failure,
// and says so in the log.
func TestUpdateReportsAFailedReadBack(t *testing.T) {
	tid := uuid.New()
	const readBack = 2 // Update's own Get is the first read.
	repo := &fakeRepo{
		sub:          admindomain.EventSubscription{TenantID: tid, SubscriptionID: uuid.New(), SinkKind: "http"},
		getFailsFrom: readBack,
		getErrLate:   errors.New("connection reset"),
	}
	h, logs := loggedHandler(repo)
	got, err := h.Update(ctxAs(tid, apiutil.RolePlatformAdmin), tid, repo.sub, 0, nil)
	if code(err) != connect.CodeInternal {
		t.Fatalf("Update = %+v, %v; want internal", got, err)
	}
	if logs.FilterLevelExact(zapcore.ErrorLevel).Len() != 1 {
		t.Fatalf("want one error line, got %v", logs.All())
	}
}

// A create the store refuses for a reason the caller can act on keeps that
// reason, rather than reading as an internal failure.
func TestCreateMapsAStoreRefusal(t *testing.T) {
	tid := uuid.New()
	h, _ := loggedHandler(&fakeRepo{createErr: admindomain.ErrAlreadyExists})
	_, err := h.Create(ctxAs(tid, apiutil.RolePlatformAdmin), admindomain.EventSubscription{TenantID: tid, SinkKind: "http"})
	if code(err) != connect.CodeAlreadyExists {
		t.Fatalf("code = %v, want AlreadyExists", code(err))
	}
}

// Each change says how it ended: done at info, refused at warn, failed at
// error, naming the subscription's tenant.
func TestSubscriptionChangesAreLogged(t *testing.T) {
	tid := uuid.New()
	sub := admindomain.EventSubscription{TenantID: tid, SubscriptionID: uuid.New(), SinkKind: "http"}
	ctx := ctxAs(tid, apiutil.RolePlatformAdmin)
	for _, tc := range []struct {
		name  string
		repo  *fakeRepo
		kinds SinkKinds
		call  func(*Handler) error
		level zapcore.Level
	}{
		{"create done", &fakeRepo{stampedID: sub.SubscriptionID}, nil, func(h *Handler) error {
			_, err := h.Create(ctx, sub)
			return err
		}, zapcore.InfoLevel},
		{"create refused, kind off", &fakeRepo{}, offKinds{}, func(h *Handler) error {
			_, err := h.Create(ctx, sub)
			return err
		}, zapcore.WarnLevel},
		{"create failed", &fakeRepo{createErr: errors.New("connection reset")}, nil, func(h *Handler) error {
			_, err := h.Create(ctx, sub)
			return err
		}, zapcore.ErrorLevel},
		{"update done", &fakeRepo{sub: sub}, nil, func(h *Handler) error {
			_, err := h.Update(ctx, tid, sub, 0, nil)
			return err
		}, zapcore.InfoLevel},
		{"delete done", &fakeRepo{sub: sub}, nil, func(h *Handler) error {
			return h.Delete(ctx, tid, sub.SubscriptionID, 0)
		}, zapcore.InfoLevel},
		{"redrive done", &fakeRepo{sub: sub, requeued: 3}, nil, func(h *Handler) error {
			_, err := h.RedriveFailedDeliveries(ctx, tid, sub.SubscriptionID)
			return err
		}, zapcore.InfoLevel},
		{"test delivery failed", &fakeRepo{sub: sub}, nil, func(h *Handler) error {
			h.SetDispatcher(failDispatcher{})
			return h.TestSubscription(ctx, tid, sub.SubscriptionID)
		}, zapcore.WarnLevel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, logs := loggedHandler(tc.repo)
			if tc.kinds != nil {
				h.SetSinkKinds(tc.kinds)
			}
			_ = tc.call(h)
			lines := logs.FilterLevelExact(tc.level).All()
			if len(lines) != 1 {
				t.Fatalf("want one %v line, got %v", tc.level, logs.All())
			}
			if lines[0].ContextMap()["tenant_id"] != tid.String() {
				t.Errorf("line %q names tenant %v, want %s", lines[0].Message, lines[0].ContextMap()["tenant_id"], tid)
			}
			// ctxAs's principal is "tester".
			if lines[0].ContextMap()["actor"] != "tester" {
				t.Errorf("line %q names actor %v, want tester", lines[0].Message, lines[0].ContextMap()["actor"])
			}
		})
	}
}

// A store answer the caller can act on — a subscription not there, a version
// that moved, one already there — is logged as refused at warn; a store fault
// as failed at error. A missing subscription read as "read failed", which
// sends an operator looking for a fault that is not there.
func TestStoreAnswersAreLoggedByWhatTheyAre(t *testing.T) {
	tid := uuid.New()
	sub := admindomain.EventSubscription{TenantID: tid, SubscriptionID: uuid.New(), SinkKind: "http"}
	ctx := ctxAs(tid, apiutil.RolePlatformAdmin)
	fault := errors.New("connection reset")
	get := func(h *Handler) error {
		_, err := h.Get(ctx, tid, sub.SubscriptionID)
		return err
	}
	create := func(h *Handler) error {
		_, err := h.Create(ctx, sub)
		return err
	}
	update := func(h *Handler) error {
		_, err := h.Update(ctx, tid, sub, 0, nil)
		return err
	}
	del := func(h *Handler) error { return h.Delete(ctx, tid, sub.SubscriptionID, 0) }
	list := func(h *Handler) error {
		_, _, err := h.List(ctx, admindomain.ListEventSubscriptionsArgs{TenantID: tid})
		return err
	}
	redrive := func(h *Handler) error {
		_, err := h.RedriveFailedDeliveries(ctx, tid, sub.SubscriptionID)
		return err
	}
	for _, tc := range []struct {
		name  string
		repo  *fakeRepo
		call  func(*Handler) error
		level zapcore.Level
		msg   string
	}{
		{"read of a missing one", &fakeRepo{getErr: admindomain.ErrNotFound}, get, zapcore.WarnLevel, "event subscription read refused"},
		{"read fault", &fakeRepo{getErr: fault}, get, zapcore.ErrorLevel, "event subscription read failed"},
		{"create of one already there", &fakeRepo{createErr: admindomain.ErrAlreadyExists}, create, zapcore.WarnLevel, "event subscription create refused"},
		{"create fault", &fakeRepo{createErr: fault}, create, zapcore.ErrorLevel, "event subscription create failed"},
		{"update of a moved version", &fakeRepo{sub: sub, updateErr: admindomain.ErrVersionMismatch}, update, zapcore.WarnLevel, "event subscription update refused"},
		{"update fault", &fakeRepo{sub: sub, updateErr: fault}, update, zapcore.ErrorLevel, "event subscription update failed"},
		{"delete of one gone since", &fakeRepo{sub: sub, deleteErr: admindomain.ErrNotFound}, del, zapcore.WarnLevel, "event subscription delete refused"},
		{"delete fault", &fakeRepo{sub: sub, deleteErr: fault}, del, zapcore.ErrorLevel, "event subscription delete failed"},
		{"list fault", &fakeRepo{listErr: fault}, list, zapcore.ErrorLevel, "event subscription list failed"},
		{"redrive of one gone since", &fakeRepo{sub: sub, requeueErr: admindomain.ErrNotFound}, redrive, zapcore.WarnLevel, "event subscription redrive refused"},
		{"redrive fault", &fakeRepo{sub: sub, requeueErr: fault}, redrive, zapcore.ErrorLevel, "event subscription redrive failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, logs := loggedHandler(tc.repo)
			if err := tc.call(h); err == nil {
				t.Fatal("the call succeeded")
			}
			lines := logs.FilterMessage(tc.msg).All()
			if len(lines) != 1 || lines[0].Level != tc.level {
				t.Fatalf("want one %v line %q, got %v", tc.level, tc.msg, logs.All())
			}
			if lines[0].ContextMap()["tenant_id"] != tid.String() {
				t.Errorf("line %q names tenant %v, want %s", tc.msg, lines[0].ContextMap()["tenant_id"], tid)
			}
		})
	}
}
