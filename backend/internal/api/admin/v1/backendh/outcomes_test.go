package backendh

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/jackc/pgx/v5"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
)

// errStore is a store failure no caller can fix.
var errStore = errors.New("connection reset by peer")

// secretRef is a credential reference no log line may carry.
const secretRef = "secret://paladin/backend-credentials-do-not-log"

// scriptedRepo is fakeBackendRepo with the reads and writes a test scripts.
type scriptedRepo struct {
	fakeBackendRepo
	getErr      error
	failGetFrom int // the Get call (1-based) from which Get fails with getErr; 0 = every call
	getCalls    *int
	listErr     error
	writeErr    error
}

func (r scriptedRepo) Get(ctx context.Context, id string) (admindomain.StorageBackend, error) {
	*r.getCalls++
	if r.getErr != nil && *r.getCalls >= r.failGetFrom {
		return admindomain.StorageBackend{}, r.getErr
	}
	b, _ := r.fakeBackendRepo.Get(ctx, id)
	b.CredentialsSecretRef = secretRef
	return b, nil
}

func (r scriptedRepo) List(context.Context, int32, string, string) ([]admindomain.StorageBackend, string, error) {
	return nil, "", r.listErr
}

func (r scriptedRepo) Update(context.Context, admindomain.StorageBackend, int64, []string) error {
	return r.writeErr
}

func (r scriptedRepo) Delete(context.Context, string, int64) error { return r.writeErr }

func (r scriptedRepo) RotateCredentialsTx(context.Context, pgx.Tx, string, string, int64) error {
	return r.writeErr
}

func scripted(r scriptedRepo) scriptedRepo {
	r.getCalls = new(int)
	return r
}

// observed is a handler over repo whose log lines at level and above are kept.
func observed(repo Repository, level zapcore.Level) (*Handler, *observer.ObservedLogs) {
	core, logs := observer.New(level)
	h := NewHandler(repo, allowAuthorizer{})
	h.SetLogger(zap.New(core))
	return h, logs
}

var platformAdmin = func() context.Context { return ctxWithRoles(apiutil.RolePlatformAdmin) }

// A store that fails is the server's fault: the answer is internal, never the
// not-found a missing backend gets, and the log says why.
func TestStoreFailuresAnswerInternal(t *testing.T) {
	for _, tc := range []struct {
		name string
		repo scriptedRepo
		call func(*Handler) error
	}{
		{"test backend", scripted(scriptedRepo{getErr: errStore}), func(h *Handler) error {
			_, err := h.TestBackend(platformAdmin(), "primary")
			return err
		}},
		{"get", scripted(scriptedRepo{getErr: errStore}), func(h *Handler) error {
			_, err := h.GetBackend(platformAdmin(), "primary")
			return err
		}},
		{"list", scripted(scriptedRepo{listErr: errStore}), func(h *Handler) error {
			_, _, err := h.ListBackends(platformAdmin(), 0, "", "")
			return err
		}},
		{"update", scripted(scriptedRepo{writeErr: errStore}), func(h *Handler) error {
			_, err := h.UpdateBackend(platformAdmin(), admindomain.StorageBackend{BackendID: "primary"}, 1, nil)
			return err
		}},
		{"update read back", scripted(scriptedRepo{getErr: errStore, failGetFrom: 1}), func(h *Handler) error {
			_, err := h.UpdateBackend(platformAdmin(), admindomain.StorageBackend{BackendID: "primary"}, 1, nil)
			return err
		}},
		{"rotate", scripted(scriptedRepo{writeErr: errStore}), func(h *Handler) error {
			_, err := h.RotateCredentials(platformAdmin(), "primary", secretRef, 0)
			return err
		}},
		{"delete", scripted(scriptedRepo{writeErr: errStore}), func(h *Handler) error {
			return h.DeleteBackend(platformAdmin(), "primary", 1)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, logs := observed(tc.repo, zapcore.ErrorLevel)
			if err := tc.call(h); connect.CodeOf(err) != connect.CodeInternal {
				t.Fatalf("err = %v, want internal", err)
			}
			all := logs.All()
			if len(all) != 1 || all[0].ContextMap()["error"] == nil || all[0].ContextMap()["backend_id"] == nil {
				t.Fatalf("want one error line with the cause and the backend, got %v", all)
			}
		})
	}
}

// TestBackend on a backend that is not there is still not found, and a
// refusal is logged at warn.
func TestTestBackendOfAMissingBackend(t *testing.T) {
	h, logs := observed(scripted(scriptedRepo{getErr: admindomain.ErrNotFound}), zapcore.WarnLevel)
	if _, err := h.TestBackend(platformAdmin(), "primary"); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("err = %v, want not found", err)
	}
	if all := logs.All(); len(all) != 1 || all[0].Level != zapcore.WarnLevel {
		t.Fatalf("want one warn line, got %v", all)
	}
}

// Every change to a backend is logged once at info, naming the backend and
// who changed it — and no line carries its credential reference.
func TestChangesAreLoggedWithoutSecrets(t *testing.T) {
	ctx := platformAdmin
	for _, tc := range []struct {
		op   string
		call func(*Handler) error
	}{
		{opCreate, func(h *Handler) error {
			_, err := h.CreateBackend(ctx(), admindomain.StorageBackend{BackendID: "primary", Kind: "s3-compatible", CredentialsSecretRef: secretRef})
			return err
		}},
		{opUpdate, func(h *Handler) error {
			_, err := h.UpdateBackend(ctx(), admindomain.StorageBackend{BackendID: "primary", CredentialsSecretRef: secretRef}, 1, []string{"credentials_secret_ref"})
			return err
		}},
		{opSetEnabled, func(h *Handler) error {
			_, err := h.SetBackendEnabled(ctx(), "primary", false, 1)
			return err
		}},
		{opSetReadOnly, func(h *Handler) error {
			_, err := h.SetBackendReadOnly(ctx(), "primary", true, 1)
			return err
		}},
		{opSetMaintenance, func(h *Handler) error {
			_, err := h.SetBackendMaintenance(ctx(), "primary", true, 1)
			return err
		}},
		{opRotateCredentials, func(h *Handler) error {
			_, err := h.RotateCredentials(ctx(), "primary", secretRef, time.Hour)
			return err
		}},
		{opDelete, func(h *Handler) error { return h.DeleteBackend(ctx(), "primary", 1) }},
	} {
		t.Run(tc.op, func(t *testing.T) {
			h, logs := observed(scripted(scriptedRepo{}), zapcore.DebugLevel)
			if err := tc.call(h); err != nil {
				t.Fatalf("err = %v", err)
			}
			all := logs.All()
			if len(all) != 1 || all[0].Level != zapcore.InfoLevel {
				t.Fatalf("want one info line, got %v", all)
			}
			got := all[0].ContextMap()
			for k, v := range map[string]any{"op": tc.op, "backend_id": "primary", "actor": "tester"} {
				if got[k] != v {
					t.Errorf("%s = %v, want %v", k, got[k], v)
				}
			}
			for k, v := range got {
				if s, ok := v.(string); ok && strings.Contains(s, secretRef) {
					t.Errorf("%s carries the credential reference", k)
				}
			}
		})
	}
}
