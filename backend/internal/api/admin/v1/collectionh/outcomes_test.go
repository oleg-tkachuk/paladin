package collectionh

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// errStore is a store failure no caller can fix.
var errStore = errors.New("connection reset by peer")

// observed is a handler over repo whose log lines at level and above are kept.
func observed(repo *fakeRepo, level zapcore.Level) (*Handler, *observer.ObservedLogs) {
	core, logs := observer.New(level)
	h := NewHandler(repo, allowAll())
	h.SetLogger(zap.New(core))
	return h, logs
}

// wantOneLine fails unless logs holds exactly one line at level, naming the
// cause and the tenant.
func wantOneLine(t *testing.T, logs *observer.ObservedLogs, level zapcore.Level, tenant uuid.UUID) {
	t.Helper()
	all := logs.All()
	if len(all) != 1 || all[0].Level != level || all[0].ContextMap()["error"] == nil {
		t.Fatalf("want one %v line with the cause, got %v", level, all)
	}
	if got := all[0].ContextMap()["tenant_id"]; got != tenant.String() {
		t.Errorf("tenant_id = %v, want %s", got, tenant)
	}
}

// A store that fails is the server's fault: the answer is internal, and the
// log says why.
func TestStoreFailuresAnswerInternal(t *testing.T) {
	tid := uuid.New()
	missing := func(context.Context, uuid.UUID, string) (Collection, error) {
		return Collection{}, ErrCollectionNotFound
	}
	for _, tc := range []struct {
		name string
		repo *fakeRepo
		call func(*Handler) error
	}{
		{"list", &fakeRepo{listFn: func(context.Context, ListCollectionsArgs) ([]Collection, string, error) {
			return nil, "", errStore
		}}, func(h *Handler) error {
			_, _, err := h.ListCollections(authedCtx(tid), ListCollectionsArgs{})
			return err
		}},
		{"get", &fakeRepo{getFn: func(context.Context, uuid.UUID, string) (Collection, error) {
			return Collection{}, errStore
		}}, func(h *Handler) error {
			_, err := h.GetCollection(authedCtx(tid), uuid.Nil(), "k")
			return err
		}},
		{"usage", &fakeRepo{usageFn: func(context.Context, []CollectionKey) (map[CollectionKey]CollectionUsage, error) {
			return nil, errStore
		}}, func(h *Handler) error {
			_, err := h.GetCollection(authedCtx(tid), uuid.Nil(), "k")
			return err
		}},
		{"ensure read", &fakeRepo{getFn: func(context.Context, uuid.UUID, string) (Collection, error) {
			return Collection{}, errStore
		}}, func(h *Handler) error {
			_, err := h.EnsureCollection(authedCtx(tid), CreateCollectionArgs{Collection: "k", BackendID: "b", BucketName: "n"})
			return err
		}},
		{"ensure write", &fakeRepo{getFn: missing, createTxFn: func(context.Context, CreateCollectionArgs) (Collection, error) {
			return Collection{}, errStore
		}}, func(h *Handler) error {
			_, err := h.EnsureCollection(authedCtx(tid), CreateCollectionArgs{Collection: "k", BackendID: "b", BucketName: "n"})
			return err
		}},
		{"rebind", &fakeRepo{rebindFn: func(context.Context, uuid.UUID, string, string, string, int64) error {
			return errStore
		}}, func(h *Handler) error {
			_, err := h.BindCollectionToBucket(authedCtx(tid), uuid.Nil(), "k", "storageBackends/aws-eu/buckets/paladin-prod", 0)
			return err
		}},
		{"update", &fakeRepo{updateTxFn: func(context.Context, UpdateCollectionArgs) (Collection, error) {
			return Collection{}, errStore
		}}, func(h *Handler) error {
			_, err := h.UpdateCollection(authedCtx(tid), UpdateCollectionArgs{Collection: "k"})
			return err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h, logs := observed(tc.repo, zapcore.ErrorLevel)
			if err := tc.call(h); connect.CodeOf(err) != connect.CodeInternal {
				t.Fatalf("err = %v, want internal", err)
			}
			wantOneLine(t, logs, zapcore.ErrorLevel, tid)
		})
	}
}

// A self-provisioned collection naming a bucket that is not registered is a
// missing resource, as it is for CreateCollection; it used to answer internal.
func TestEnsureCollectionOnAnUnknownBucket(t *testing.T) {
	tid := uuid.New()
	h, logs := observed(&fakeRepo{
		getFn: func(context.Context, uuid.UUID, string) (Collection, error) {
			return Collection{}, ErrCollectionNotFound
		},
		createTxFn: func(context.Context, CreateCollectionArgs) (Collection, error) {
			return Collection{}, fmt.Errorf("%w: b/gone", ErrBucketNotFound)
		},
	}, zapcore.WarnLevel)
	_, err := h.EnsureCollection(authedCtx(tid), CreateCollectionArgs{Collection: "k", BackendID: "b", BucketName: "gone"})
	wantCode(t, err, connect.CodeNotFound)
	wantOneLine(t, logs, zapcore.WarnLevel, tid)
}

// A delete the objects refuse stays refused when they cannot be counted, and
// the failed count is logged rather than lost.
func TestNotEmptyCountFailureIsLogged(t *testing.T) {
	tid := uuid.New()
	h, logs := observed(&fakeRepo{
		getFn: func(_ context.Context, tenant uuid.UUID, key string) (Collection, error) {
			return fullKey(tenant, key), nil
		},
		deleteTxFn: func(context.Context, uuid.UUID, string, int64) error { return ErrCollectionHasObjects },
		holdingFn: func(context.Context, uuid.UUID, string) (CollectionHolding, error) {
			return CollectionHolding{}, errStore
		},
	}, zapcore.ErrorLevel)
	err := h.DeleteCollection(authedCtx(tid), uuid.Nil(), "k", 1)
	wantCode(t, err, connect.CodeFailedPrecondition)
	wantOneLine(t, logs, zapcore.ErrorLevel, tid)
}
