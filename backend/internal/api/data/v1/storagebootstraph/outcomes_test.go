package storagebootstraph

import (
	"errors"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func observedHandler(backends BackendChecker, buckets BucketEnsurer, keys CollectionEnsurer) (*Handler, *observer.ObservedLogs) {
	core, logs := observer.New(zapcore.DebugLevel)
	h := newHandler(&recordingAuthorizer{allow: true}, backends, buckets, keys)
	h.SetLogger(zap.New(core))
	return h, logs
}

// A provisioning is a change to the tenant's storage, so it says what it
// created, for which tenant, and on whose token.
func TestEnsureTenantStorageLogsWhatItProvisioned(t *testing.T) {
	caller := uuid.New()
	keys := &fakeCollections{createdKeys: map[string]bool{"docs": true}}
	h, logs := observedHandler(&fakeBackends{enabled: true}, &fakeBuckets{created: true}, keys)
	if _, err := h.EnsureTenantStorage(ctxWithPAT(caller), backendID, bucket, []string{"docs", "", "reports"}); err != nil {
		t.Fatal(err)
	}
	lines := logs.FilterLevelExact(zapcore.InfoLevel).All()
	if len(lines) != 1 {
		t.Fatalf("want one info line, got %v", logs.All())
	}
	f := lines[0].ContextMap()
	if f["tenant_id"] != caller.String() || f["backend_id"] != backendID || f["bucket_name"] != bucket ||
		f["bucket_created"] != true || f["actor"] == "" {
		t.Errorf("line = %v", f)
	}
	if created, ok := f["collections_created"].([]any); !ok || len(created) != 1 || created[0] != "docs" {
		t.Errorf("collections_created = %v, want [docs]", f["collections_created"])
	}
	if existing, ok := f["collections_existing"].([]any); !ok || len(existing) != 1 || existing[0] != "reports" {
		t.Errorf("collections_existing = %v, want [reports]", f["collections_existing"])
	}
}

func TestEnsureTenantStorageBackendAnswers(t *testing.T) {
	for _, tc := range []struct {
		name     string
		backends *fakeBackends
		code     connect.Code
		level    zapcore.Level
	}{
		{"backend disabled", &fakeBackends{enabled: false}, connect.CodeFailedPrecondition, zapcore.WarnLevel},
		{"store failing", &fakeBackends{err: errors.New("connection reset")}, connect.CodeInternal, zapcore.ErrorLevel},
	} {
		t.Run(tc.name, func(t *testing.T) {
			buckets := &fakeBuckets{}
			h, logs := observedHandler(tc.backends, buckets, &fakeCollections{})
			_, err := h.EnsureTenantStorage(ctxWithPAT(uuid.New()), backendID, bucket, nil)
			if connect.CodeOf(err) != tc.code {
				t.Fatalf("code = %v, want %v", connect.CodeOf(err), tc.code)
			}
			if buckets.calls != 0 {
				t.Error("the bucket was ensured past a refused backend")
			}
			if logs.FilterLevelExact(tc.level).FilterField(zap.String("backend_id", backendID)).Len() != 1 {
				t.Errorf("want one %v line naming the backend, got %v", tc.level, logs.All())
			}
		})
	}
}

func TestEnsureTenantStorageRequiresBackendAndBucket(t *testing.T) {
	for _, tc := range []struct{ name, backend, bucket string }{
		{"no backend", "", bucket},
		{"no bucket", backendID, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			backends := &fakeBackends{enabled: true}
			h := newHandler(&recordingAuthorizer{allow: true}, backends, &fakeBuckets{}, &fakeCollections{})
			_, err := h.EnsureTenantStorage(ctxWithPAT(uuid.New()), tc.backend, tc.bucket, nil)
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("code = %v, want InvalidArgument", connect.CodeOf(err))
			}
			if backends.gotID != "" {
				t.Error("the backend was looked up for an incomplete request")
			}
		})
	}
}

// A collection that cannot be ensured stops the run with its answer, and the
// collections after it are not tried.
func TestEnsureTenantStorageStopsAtAFailingCollection(t *testing.T) {
	keys := &fakeCollections{err: connect.NewError(connect.CodeAlreadyExists, "taken")}
	h, _ := observedHandler(&fakeBackends{enabled: true}, &fakeBuckets{}, keys)
	_, err := h.EnsureTenantStorage(ctxWithPAT(uuid.New()), backendID, bucket, []string{"docs", "reports"})
	if connect.CodeOf(err) != connect.CodeAlreadyExists {
		t.Fatalf("code = %v, want AlreadyExists", connect.CodeOf(err))
	}
}
