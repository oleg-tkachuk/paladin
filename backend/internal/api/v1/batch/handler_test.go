package batch

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/capability"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
)

// fakeSubmitter is an in-memory Submitter. It records what the handler
// enqueues so tests can assert the op-type + tenant-stamped metadata
// contract without a database or the operations worker.
type fakeSubmitter struct {
	id       uuid.UUID
	err      error
	called   bool
	lastType string
	lastMeta []byte
}

func (f *fakeSubmitter) Submit(_ context.Context, opType string, metadata []byte) (uuid.UUID, error) {
	f.called = true
	f.lastType = opType
	f.lastMeta = metadata
	return f.id, f.err
}

// fakeBuckets resolves object-key → (backend, bucket) for the submit-time
// scope check. byKey overrides per object-key; unset keys fall back to a
// fixed default so tests that don't care get a stable binding.
type fakeBuckets struct {
	byKey map[string][2]string // objectKey -> {backendID, bucket}
	err   error
}

func (f fakeBuckets) LookupBucket(_ context.Context, _ uuid.UUID, objectKey string, _ bool) (string, string, error) {
	if f.err != nil {
		return "", "", f.err
	}
	if v, ok := f.byKey[objectKey]; ok {
		return v[0], v[1], nil
	}
	return "backend-def", "bucket-def", nil
}

// newHandler wires a Handler with a default bucket resolver. Tests that assert
// on the resolved bucket pass an explicit resolver via NewHandler directly.
func newHandler(sub Submitter, az cedar.Authorizer) *Handler {
	return NewHandler(sub, az, fakeBuckets{})
}

// fakeAuthorizer is a configurable cedar.Authorizer. fn decides the
// verdict per action so BatchCopy (two authorize calls) can allow the
// source check and deny the destination one. It records the actions and the
// resources (in call order) for assertions.
type fakeAuthorizer struct {
	fn        func(action string) (cedar.Decision, error)
	actions   []string
	resources []*cedar.Resource
}

func (f *fakeAuthorizer) IsAuthorized(_ context.Context, _ *cedar.Principal, action string, r *cedar.Resource, _ cedar.RequestContext) (cedar.Decision, error) {
	f.actions = append(f.actions, action)
	if r != nil {
		cp := *r
		f.resources = append(f.resources, &cp)
	}
	if f.fn != nil {
		return f.fn(action)
	}
	return cedar.DecisionAllow, nil
}

func allowAll() *fakeAuthorizer {
	return &fakeAuthorizer{fn: func(string) (cedar.Decision, error) { return cedar.DecisionAllow, nil }}
}

// denyAction denies exactly one action and allows every other.
func denyAction(deny string) *fakeAuthorizer {
	return &fakeAuthorizer{fn: func(a string) (cedar.Decision, error) {
		if a == deny {
			return cedar.DecisionDeny, nil
		}
		return cedar.DecisionAllow, nil
	}}
}

// engineErr simulates a policy-engine fault (fetch/compile), which the
// handler must surface as CodeInternal, distinct from a plain deny.
func engineErr() *fakeAuthorizer {
	return &fakeAuthorizer{fn: func(string) (cedar.Decision, error) {
		return cedar.DecisionDeny, errors.New("policy fetch failed")
	}}
}

func authedCtx(tid uuid.UUID) context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u1", TenantID: tid})
}

// authedCtxWithCap authorises exactly `ops`; a missing op drives the
// PermissionDenied branch of auth.AssertCapabilityOp.
func authedCtxWithCap(tid uuid.UUID, ops ...capability.Op) context.Context {
	return auth.WithCapability(authedCtx(tid), &capability.Capability{
		Caveats: capability.Caveats{Ops: ops},
	})
}

func wantCode(t *testing.T, err error, want connect.Code) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with code %v, got nil", want)
	}
	if got := connect.CodeOf(err); got != want {
		t.Fatalf("error code: got %v, want %v (err=%v)", got, want, err)
	}
}

func ids(n int) []uuid.UUID {
	out := make([]uuid.UUID, n)
	for i := range out {
		out[i] = uuid.New()
	}
	return out
}

func TestBatchDelete(t *testing.T) {
	tid := uuid.New()

	t.Run("unauthenticated (no principal)", func(t *testing.T) {
		h := newHandler(&fakeSubmitter{}, allowAll())
		_, err := h.BatchDelete(context.Background(), BatchDeleteArgs{ObjectIDs: ids(1)})
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("principal without tenant → unauthenticated", func(t *testing.T) {
		ctx := auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u1"})
		h := newHandler(&fakeSubmitter{}, allowAll())
		_, err := h.BatchDelete(ctx, BatchDeleteArgs{ObjectIDs: ids(1)})
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("empty object_ids → invalid argument", func(t *testing.T) {
		h := newHandler(&fakeSubmitter{}, allowAll())
		_, err := h.BatchDelete(authedCtx(tid), BatchDeleteArgs{})
		wantCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("batch too large → invalid argument", func(t *testing.T) {
		sub := &fakeSubmitter{}
		h := newHandler(sub, allowAll())
		_, err := h.BatchDelete(authedCtx(tid), BatchDeleteArgs{ObjectIDs: make([]uuid.UUID, maxBatchSize+1)})
		wantCode(t, err, connect.CodeInvalidArgument)
		if sub.called {
			t.Fatal("submitter must not be called when batch is oversized")
		}
	})

	t.Run("capability lacks delete op → permission denied", func(t *testing.T) {
		ctx := authedCtxWithCap(tid, capability.OpGet) // no OpDelete
		h := newHandler(&fakeSubmitter{}, allowAll())
		_, err := h.BatchDelete(ctx, BatchDeleteArgs{ObjectIDs: ids(1)})
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("policy denies → permission denied", func(t *testing.T) {
		h := newHandler(&fakeSubmitter{}, denyAction(cedar.ActionDeleteObject))
		_, err := h.BatchDelete(authedCtx(tid), BatchDeleteArgs{ObjectIDs: ids(1), ObjectKey: "docs"})
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("policy engine error → internal", func(t *testing.T) {
		h := newHandler(&fakeSubmitter{}, engineErr())
		_, err := h.BatchDelete(authedCtx(tid), BatchDeleteArgs{ObjectIDs: ids(1), ObjectKey: "docs"})
		wantCode(t, err, connect.CodeInternal)
	})

	t.Run("submitter error propagates", func(t *testing.T) {
		sub := &fakeSubmitter{err: connect.NewError(connect.CodeUnavailable, errors.New("db down"))}
		h := newHandler(sub, allowAll())
		_, err := h.BatchDelete(authedCtx(tid), BatchDeleteArgs{ObjectIDs: ids(1)})
		wantCode(t, err, connect.CodeUnavailable)
	})

	t.Run("ok stamps tenant, enqueues BatchDelete, authorizes DeleteObject", func(t *testing.T) {
		opID := uuid.New()
		sub := &fakeSubmitter{id: opID}
		az := allowAll()
		objs := ids(3)
		got, err := newHandler(sub, az).BatchDelete(authedCtx(tid), BatchDeleteArgs{
			ObjectKey: "docs",
			ObjectIDs: objs,
			TenantID:  uuid.New(), // must be overwritten by ctx tenant
		})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if got != opID {
			t.Fatalf("operation id: got %v want %v", got, opID)
		}
		if !sub.called || sub.lastType != "BatchDelete" {
			t.Fatalf("submit: called=%v type=%q", sub.called, sub.lastType)
		}
		var md BatchDeleteArgs
		if err := json.Unmarshal(sub.lastMeta, &md); err != nil {
			t.Fatalf("metadata not valid JSON: %v", err)
		}
		if md.TenantID != tid {
			t.Fatalf("tenant not stamped from ctx: got %v want %v", md.TenantID, tid)
		}
		if md.ObjectKey != "docs" || len(md.ObjectIDs) != 3 || md.ObjectIDs[0] != objs[0] {
			t.Fatalf("metadata mismatch: %+v", md)
		}
		if len(az.actions) != 1 || az.actions[0] != cedar.ActionDeleteObject {
			t.Fatalf("authorized actions: %v", az.actions)
		}
	})

	t.Run("capability with delete op is allowed", func(t *testing.T) {
		ctx := authedCtxWithCap(tid, capability.OpDelete)
		sub := &fakeSubmitter{id: uuid.New()}
		_, err := newHandler(sub, allowAll()).BatchDelete(ctx, BatchDeleteArgs{ObjectIDs: ids(1)})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if !sub.called {
			t.Fatal("expected submit with valid capability op")
		}
	})
}

func TestBatchCopy(t *testing.T) {
	tid := uuid.New()

	t.Run("unauthenticated", func(t *testing.T) {
		h := newHandler(&fakeSubmitter{}, allowAll())
		_, err := h.BatchCopy(context.Background(), BatchCopyArgs{ObjectIDs: ids(1)})
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("empty object_ids → invalid argument", func(t *testing.T) {
		h := newHandler(&fakeSubmitter{}, allowAll())
		_, err := h.BatchCopy(authedCtx(tid), BatchCopyArgs{})
		wantCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("batch too large → invalid argument", func(t *testing.T) {
		h := newHandler(&fakeSubmitter{}, allowAll())
		_, err := h.BatchCopy(authedCtx(tid), BatchCopyArgs{ObjectIDs: make([]uuid.UUID, maxBatchSize+1)})
		wantCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("capability lacks put op → permission denied", func(t *testing.T) {
		ctx := authedCtxWithCap(tid, capability.OpGet) // no OpPut
		h := newHandler(&fakeSubmitter{}, allowAll())
		_, err := h.BatchCopy(ctx, BatchCopyArgs{ObjectIDs: ids(1)})
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("source copy denied → permission denied", func(t *testing.T) {
		az := denyAction(cedar.ActionCopyObject)
		h := newHandler(&fakeSubmitter{}, az)
		_, err := h.BatchCopy(authedCtx(tid), BatchCopyArgs{ObjectIDs: ids(1), SrcObjectKey: "src", DstObjectKey: "dst"})
		wantCode(t, err, connect.CodePermissionDenied)
		// Source is checked first; destination check must not run.
		if len(az.actions) != 1 || az.actions[0] != cedar.ActionCopyObject {
			t.Fatalf("expected only CopyObject checked, got %v", az.actions)
		}
	})

	t.Run("destination put denied → permission denied", func(t *testing.T) {
		az := denyAction(cedar.ActionPutObject)
		sub := &fakeSubmitter{}
		h := newHandler(sub, az)
		_, err := h.BatchCopy(authedCtx(tid), BatchCopyArgs{ObjectIDs: ids(1), SrcObjectKey: "src", DstObjectKey: "dst"})
		wantCode(t, err, connect.CodePermissionDenied)
		// Both actions evaluated, in order, before the deny.
		if len(az.actions) != 2 || az.actions[0] != cedar.ActionCopyObject || az.actions[1] != cedar.ActionPutObject {
			t.Fatalf("authorized actions: %v", az.actions)
		}
		if sub.called {
			t.Fatal("submitter must not be called on authorization failure")
		}
	})

	t.Run("ok enqueues BatchCopy with src/dst/prefix + tenant", func(t *testing.T) {
		opID := uuid.New()
		sub := &fakeSubmitter{id: opID}
		objs := ids(2)
		got, err := newHandler(sub, allowAll()).BatchCopy(authedCtx(tid), BatchCopyArgs{
			SrcObjectKey: "src",
			DstObjectKey: "dst",
			KeyPrefix:    "archive/",
			ObjectIDs:    objs,
		})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if got != opID || sub.lastType != "BatchCopy" {
			t.Fatalf("op id/type: %v %q", got, sub.lastType)
		}
		var md BatchCopyArgs
		if err := json.Unmarshal(sub.lastMeta, &md); err != nil {
			t.Fatalf("metadata not valid JSON: %v", err)
		}
		if md.TenantID != tid {
			t.Fatalf("tenant not stamped: got %v want %v", md.TenantID, tid)
		}
		if md.SrcObjectKey != "src" || md.DstObjectKey != "dst" || md.KeyPrefix != "archive/" {
			t.Fatalf("metadata mismatch: %+v", md)
		}
		if len(md.ObjectIDs) != 2 || md.ObjectIDs[1] != objs[1] {
			t.Fatalf("object ids mismatch: %+v", md.ObjectIDs)
		}
	})
}

func TestBatchUpdateTags(t *testing.T) {
	tid := uuid.New()

	t.Run("unauthenticated", func(t *testing.T) {
		h := newHandler(&fakeSubmitter{}, allowAll())
		_, err := h.BatchUpdateTags(context.Background(), BatchUpdateTagsArgs{ObjectIDs: ids(1)})
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("empty object_ids → invalid argument", func(t *testing.T) {
		h := newHandler(&fakeSubmitter{}, allowAll())
		_, err := h.BatchUpdateTags(authedCtx(tid), BatchUpdateTagsArgs{Tags: map[string]string{"k": "v"}})
		wantCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("capability lacks tag op → permission denied", func(t *testing.T) {
		ctx := authedCtxWithCap(tid, capability.OpGet) // no OpTag
		h := newHandler(&fakeSubmitter{}, allowAll())
		_, err := h.BatchUpdateTags(ctx, BatchUpdateTagsArgs{ObjectIDs: ids(1)})
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("policy denies UpdateObject → permission denied", func(t *testing.T) {
		h := newHandler(&fakeSubmitter{}, denyAction(cedar.ActionUpdateObject))
		_, err := h.BatchUpdateTags(authedCtx(tid), BatchUpdateTagsArgs{ObjectIDs: ids(1), ObjectKey: "docs"})
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("ok enqueues BatchUpdateTags with tags + tenant", func(t *testing.T) {
		opID := uuid.New()
		sub := &fakeSubmitter{id: opID}
		az := allowAll()
		got, err := newHandler(sub, az).BatchUpdateTags(authedCtx(tid), BatchUpdateTagsArgs{
			ObjectKey: "docs",
			ObjectIDs: ids(1),
			Tags:      map[string]string{"env": "prod"},
		})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if got != opID || sub.lastType != "BatchUpdateTags" {
			t.Fatalf("op id/type: %v %q", got, sub.lastType)
		}
		var md BatchUpdateTagsArgs
		if err := json.Unmarshal(sub.lastMeta, &md); err != nil {
			t.Fatalf("metadata not valid JSON: %v", err)
		}
		if md.TenantID != tid || md.Tags["env"] != "prod" {
			t.Fatalf("metadata mismatch: %+v", md)
		}
		if len(az.actions) != 1 || az.actions[0] != cedar.ActionUpdateObject {
			t.Fatalf("authorized actions: %v", az.actions)
		}
	})

	t.Run("no maxBatchSize cap on tags: oversized batch still accepted", func(t *testing.T) {
		// BatchUpdateTags intentionally omits the maxBatchSize guard the
		// other three handlers apply. Lock that behavioural difference in.
		sub := &fakeSubmitter{id: uuid.New()}
		_, err := newHandler(sub, allowAll()).BatchUpdateTags(authedCtx(tid), BatchUpdateTagsArgs{
			ObjectIDs: make([]uuid.UUID, maxBatchSize+1),
		})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if !sub.called {
			t.Fatal("expected submit despite oversized batch (no cap on tags)")
		}
	})
}

func TestBatchRestoreObjects(t *testing.T) {
	tid := uuid.New()

	t.Run("unauthenticated", func(t *testing.T) {
		h := newHandler(&fakeSubmitter{}, allowAll())
		_, err := h.BatchRestoreObjects(context.Background(), BatchRestoreObjectsArgs{ObjectIDs: ids(1)})
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("empty object_ids → invalid argument", func(t *testing.T) {
		h := newHandler(&fakeSubmitter{}, allowAll())
		_, err := h.BatchRestoreObjects(authedCtx(tid), BatchRestoreObjectsArgs{})
		wantCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("batch too large → invalid argument", func(t *testing.T) {
		h := newHandler(&fakeSubmitter{}, allowAll())
		_, err := h.BatchRestoreObjects(authedCtx(tid), BatchRestoreObjectsArgs{ObjectIDs: make([]uuid.UUID, maxBatchSize+1)})
		wantCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("capability lacks put op → permission denied", func(t *testing.T) {
		ctx := authedCtxWithCap(tid, capability.OpGet) // restore requires OpPut
		h := newHandler(&fakeSubmitter{}, allowAll())
		_, err := h.BatchRestoreObjects(ctx, BatchRestoreObjectsArgs{ObjectIDs: ids(1)})
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("policy denies RestoreObject → permission denied", func(t *testing.T) {
		h := newHandler(&fakeSubmitter{}, denyAction(cedar.ActionRestoreObject))
		_, err := h.BatchRestoreObjects(authedCtx(tid), BatchRestoreObjectsArgs{ObjectIDs: ids(1), ObjectKey: "docs"})
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("ok enqueues BatchRestoreObjects, authorizes RestoreObject", func(t *testing.T) {
		opID := uuid.New()
		sub := &fakeSubmitter{id: opID}
		az := allowAll()
		got, err := newHandler(sub, az).BatchRestoreObjects(authedCtx(tid), BatchRestoreObjectsArgs{
			ObjectKey: "docs",
			ObjectIDs: ids(4),
		})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if got != opID || sub.lastType != "BatchRestoreObjects" {
			t.Fatalf("op id/type: %v %q", got, sub.lastType)
		}
		var md BatchRestoreObjectsArgs
		if err := json.Unmarshal(sub.lastMeta, &md); err != nil {
			t.Fatalf("metadata not valid JSON: %v", err)
		}
		if md.TenantID != tid || md.ObjectKey != "docs" || len(md.ObjectIDs) != 4 {
			t.Fatalf("metadata mismatch: %+v", md)
		}
		if len(az.actions) != 1 || az.actions[0] != cedar.ActionRestoreObject {
			t.Fatalf("authorized actions: %v", az.actions)
		}
	})
}

// The submit-time authz Resource must carry the resolved (backend, bucket) for
// EACH target object-key so a bucket:/object_key:-scoped PAT enforces — the
// batch worker does not re-check Cedar per object.
func TestBatchAuthzResourceCarriesBucket(t *testing.T) {
	tid := uuid.New()
	az := allowAll()
	buckets := fakeBuckets{byKey: map[string][2]string{
		"src": {"be-s", "bucket-src"},
		"dst": {"be-d", "bucket-dst"},
	}}
	_, err := NewHandler(&fakeSubmitter{id: uuid.New()}, az, buckets).
		BatchCopy(authedCtx(tid), BatchCopyArgs{ObjectIDs: ids(1), SrcObjectKey: "src", DstObjectKey: "dst"})
	if err != nil {
		t.Fatalf("BatchCopy: %v", err)
	}
	if len(az.resources) != 2 {
		t.Fatalf("expected 2 authz resources (src, dst), got %d", len(az.resources))
	}
	if az.resources[0].BucketName != "bucket-src" || az.resources[0].BackendID != "be-s" {
		t.Fatalf("src authz Resource missing binding: %+v", az.resources[0])
	}
	if az.resources[1].BucketName != "bucket-dst" || az.resources[1].BackendID != "be-d" {
		t.Fatalf("dst authz Resource missing binding: %+v", az.resources[1])
	}
}

// permitStore is a cedar.Store returning a blanket permit, so only the
// scope-enforcement built-in decides — exercising the REAL engine end-to-end.
type permitStore struct{}

func (permitStore) Fetch(context.Context, uuid.UUID, string) (string, []byte, string, error) {
	return "permit(principal, action, resource);", nil, "", nil
}
func (permitStore) Watch(context.Context) (<-chan cedar.ChangeEvent, error) { return nil, nil }

func scopedCtx(tid uuid.UUID, scopes ...auth.Scope) context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{
		Subject: "svc", TenantID: tid, Scopes: scopes,
	})
}

// End-to-end against the real Cedar engine: a bucket:/object_key:-scoped PAT
// may submit a batch confined to its scoped object-key, and is DENIED when any
// target object-key is off-scope (BatchCopy dst).
func TestBatchScopeEnforcedByEngine(t *testing.T) {
	tid := uuid.New()
	engine := cedar.NewEngine(permitStore{}, time.Minute)
	buckets := fakeBuckets{byKey: map[string][2]string{
		"docs":   {"be", "bkt"},
		"secret": {"be", "bkt"},
	}}
	okScope := auth.Scope{Type: auth.ScopeObjectKey, Value: "bkt/docs"}

	t.Run("allow on scoped object-key", func(t *testing.T) {
		sub := &fakeSubmitter{id: uuid.New()}
		_, err := NewHandler(sub, engine, buckets).
			BatchDelete(scopedCtx(tid, okScope), BatchDeleteArgs{ObjectKey: "docs", ObjectIDs: ids(1)})
		if err != nil {
			t.Fatalf("scoped PAT on its own object-key must be allowed, got %v", err)
		}
		if !sub.called {
			t.Fatal("expected submit for in-scope batch")
		}
	})

	t.Run("deny off scoped object-key", func(t *testing.T) {
		sub := &fakeSubmitter{id: uuid.New()}
		_, err := NewHandler(sub, engine, buckets).
			BatchDelete(scopedCtx(tid, okScope), BatchDeleteArgs{ObjectKey: "secret", ObjectIDs: ids(1)})
		wantCode(t, err, connect.CodePermissionDenied)
		if sub.called {
			t.Fatal("submitter must not run for an off-scope batch")
		}
	})

	t.Run("deny whole submit when ANY target off-scope (copy dst)", func(t *testing.T) {
		sub := &fakeSubmitter{id: uuid.New()}
		_, err := NewHandler(sub, engine, buckets).
			BatchCopy(scopedCtx(tid, okScope), BatchCopyArgs{
				ObjectIDs: ids(1), SrcObjectKey: "docs", DstObjectKey: "secret",
			})
		wantCode(t, err, connect.CodePermissionDenied)
		if sub.called {
			t.Fatal("submitter must not run when a copy target is off-scope")
		}
	})
}
