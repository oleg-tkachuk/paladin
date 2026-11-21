package bucketh

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

// errStore is a store failure no caller can fix.
var errStore = errors.New("connection reset by peer")

// observed is a handler over repo whose log lines at level and above are kept.
func observed(repo *fakeRepo, level zapcore.Level) (*Handler, *observer.ObservedLogs) {
	core, logs := observer.New(level)
	h := NewHandler(repo, okProvisioner{onBackend: true}, allowAuthorizer{})
	h.SetLogger(zap.New(core))
	return h, logs
}

// wantOneLine fails unless logs holds exactly one line at level, naming the
// cause.
func wantOneLine(t *testing.T, logs *observer.ObservedLogs, level zapcore.Level) {
	t.Helper()
	all := logs.All()
	if len(all) != 1 || all[0].Level != level || all[0].ContextMap()["error"] == nil {
		t.Fatalf("want one %v line with the cause, got %v", level, all)
	}
}

// A store that fails is the server's fault: every such answer is internal,
// never the code the call would have had on a missing or stale row, and says
// why in the log.
func TestStoreFailuresAnswerInternal(t *testing.T) {
	admin := ctxAs(apiutil.RoleBucketAdmin)
	for _, tc := range []struct {
		name string
		repo *fakeRepo
		call func(*Handler) error
	}{
		{"list", &fakeRepo{listErr: errStore}, func(h *Handler) error {
			_, _, err := h.ListBuckets(admin, admindomain.ListBucketsArgs{})
			return err
		}},
		{"list accessible", &fakeRepo{listAccErr: errStore}, func(h *Handler) error {
			_, _, err := h.ListAccessibleBuckets(ctxAs(apiutil.RolePlatformAdmin), uuid.New(), 0, "", "")
			return err
		}},
		{"read before a setter", &fakeRepo{getErr: errStore}, func(h *Handler) error {
			_, err := h.SetPolicy(admin, "primary", "acme", "", 1)
			return err
		}},
		{"policy read back", &fakeRepo{getErr: errStore, failGetFrom: 2}, func(h *Handler) error {
			_, err := h.SetPolicy(admin, "primary", "acme", "", 1)
			return err
		}},
		{"lifecycle read back", &fakeRepo{getErr: errStore, failGetFrom: 2}, func(h *Handler) error {
			_, err := h.SetLifecycleRules(admin, "primary", "acme", nil, 1)
			return err
		}},
		{"object lock read back", &fakeRepo{getErr: errStore, failGetFrom: 2}, func(h *Handler) error {
			_, err := h.SetObjectLock(admin, "primary", "acme", admindomain.ObjectLockConfig{}, 1)
			return err
		}},
		{"versioning read back", &fakeRepo{getErr: errStore, failGetFrom: 2}, func(h *Handler) error {
			_, err := h.SetVersioning(admin, "primary", "acme", admindomain.BucketVersioning{Enabled: true}, 1)
			return err
		}},
		{"replication read back", &fakeRepo{getErr: errStore, failGetFrom: 2}, func(h *Handler) error {
			_, err := h.SetReplication(admin, "primary", "acme", admindomain.BucketReplication{}, 1)
			return err
		}},
		{"reference count", &fakeRepo{bucketRefsErr: errStore}, func(h *Handler) error {
			return h.DeleteBucket(admin, DeleteBucketInput{BackendID: "primary", BucketName: "acme"})
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, logs := observed(tc.repo, zapcore.ErrorLevel)
			if err := tc.call(h); connect.CodeOf(err) != connect.CodeInternal {
				t.Fatalf("err = %v, want internal", err)
			}
			wantOneLine(t, logs, zapcore.ErrorLevel)
		})
	}
}

// A refusal the caller can act on keeps its code and is logged at warn.
func TestRefusalsAreLoggedAtWarn(t *testing.T) {
	h, logs := observed(&fakeRepo{setPolicyErr: admindomain.ErrVersionMismatch}, zapcore.WarnLevel)
	_, err := h.SetPolicy(ctxAs(apiutil.RoleBucketAdmin), "primary", "acme", "", 1)
	if connect.CodeOf(err) != connect.CodeAborted {
		t.Fatalf("err = %v, want aborted", err)
	}
	wantOneLine(t, logs, zapcore.WarnLevel)
	if got := logs.All()[0].ContextMap()["bucket_name"]; got != "acme" {
		t.Errorf("bucket_name = %v, want acme", got)
	}
}
