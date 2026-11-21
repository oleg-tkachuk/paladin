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

// Every change to a bucket is logged once at info: what changed, on which
// bucket, under which owner, and who changed it.
func TestChangesAreLogged(t *testing.T) {
	stored := validBucket()
	stored.Versioning.Enabled = true
	admin := ctxAs(apiutil.RoleBucketAdmin)
	grantee := uuid.New()
	for _, tc := range []struct {
		op   string
		call func(*Handler) error
	}{
		{opCreate, func(h *Handler) error {
			_, err := h.CreateBucket(admin, CreateBucketInput{Bucket: stored})
			return err
		}},
		{opEnsure, func(h *Handler) error {
			_, _, err := h.EnsureBucket(admin, CreateBucketInput{Bucket: stored})
			return err
		}},
		{opUpdate, func(h *Handler) error {
			_, err := h.UpdateBucket(admin, UpdateBucketInput{Bucket: stored, ExpectedVersion: 1})
			return err
		}},
		{opDelete, func(h *Handler) error {
			return h.DeleteBucket(admin, DeleteBucketInput{BackendID: stored.BackendID, BucketName: stored.BucketName})
		}},
		{opSetPolicy, func(h *Handler) error {
			_, err := h.SetPolicy(admin, stored.BackendID, stored.BucketName, "", 1)
			return err
		}},
		{opSetLifecycle, func(h *Handler) error {
			_, err := h.SetLifecycleRules(admin, stored.BackendID, stored.BucketName, nil, 1)
			return err
		}},
		{opSetObjectLock, func(h *Handler) error {
			_, err := h.SetObjectLock(admin, stored.BackendID, stored.BucketName, admindomain.ObjectLockConfig{Enabled: true}, 1)
			return err
		}},
		{opSetVersioning, func(h *Handler) error {
			_, err := h.SetVersioning(admin, stored.BackendID, stored.BucketName, admindomain.BucketVersioning{Enabled: true}, 1)
			return err
		}},
		{opSetReplication, func(h *Handler) error {
			_, err := h.SetReplication(admin, stored.BackendID, stored.BucketName, admindomain.BucketReplication{}, 1)
			return err
		}},
		{opGrant, func(h *Handler) error {
			_, err := h.GrantBucket(admin, stored.BackendID, stored.BucketName, grantee)
			return err
		}},
		{opRevokeGrant, func(h *Handler) error {
			return h.RevokeBucketGrant(admin, stored.BackendID, stored.BucketName, grantee)
		}},
	} {
		t.Run(tc.op, func(t *testing.T) {
			repo := &fakeRepo{getBucket: stored, getTxBucket: stored, backendEnabled: true}
			if tc.op == opCreate || tc.op == opEnsure {
				repo.getNotFoundFirst = true
			}
			core, logs := observer.New(zapcore.InfoLevel)
			h := NewHandler(repo, okProvisioner{onBackend: true}, allowAuthorizer{})
			h.SetGrants(&fakeGrants{})
			h.SetLogger(zap.New(core))
			if err := tc.call(h); err != nil {
				t.Fatalf("err = %v", err)
			}
			all := logs.All()
			if len(all) != 1 || all[0].Level != zapcore.InfoLevel {
				t.Fatalf("want one info line, got %v", all)
			}
			want := map[string]any{
				"op": tc.op, "tenant_id": stored.OwnerTenantID.String(), "actor": "tester",
				"backend_id": stored.BackendID, "bucket_name": stored.BucketName,
			}
			got := all[0].ContextMap()
			for k, v := range want {
				if got[k] != v {
					t.Errorf("%s = %v, want %v", k, got[k], v)
				}
			}
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
