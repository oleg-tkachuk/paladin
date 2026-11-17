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
