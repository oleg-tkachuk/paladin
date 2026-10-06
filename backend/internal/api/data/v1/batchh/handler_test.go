package batchh

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/capability"
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
	byKey map[string][2]string // collection -> {backendID, bucket}
	err   error
}

func (f fakeBuckets) LookupBucket(_ context.Context, _ uuid.UUID, collection string, _ bool) (string, string, error) {
	if f.err != nil {
		return "", "", f.err
	}
	if v, ok := f.byKey[collection]; ok {
		return v[0], v[1], nil
	}
	return "backend-def", "bucket-def", nil
}

// fakeObjects serves FindByIDs from a fixed set, as the store would: every
// object whose id was asked for, whatever its collection.
type fakeObjects struct {
	objs  []objecth.Object
	err   error
	calls int
}

func (f *fakeObjects) FindByIDs(_ context.Context, _ uuid.UUID, ids []uuid.UUID) ([]objecth.Object, error) {
	f.calls++
	if f.err != nil {
		return nil, f.err
	}
	var out []objecth.Object
	for _, o := range f.objs {
		if slices.Contains(ids, o.ObjectID) {
			out = append(out, o)
		}
	}
	return out, nil
}

// newHandler wires a Handler with a default bucket resolver and no objects.
// Tests that assert on the resolved bucket or the objects read pass their own
// to NewHandler directly.
func newHandler(sub Submitter, az cedar.Authorizer) *Handler {
	return NewHandler(sub, az, fakeBuckets{}, &fakeObjects{})
}

// fakeAuthorizer is a configurable cedar.Authorizer. fn decides the
// verdict per action so BatchCopy (two authorize calls) can allow the
// source check and deny the destination one. It records the actions and the
// resources (in call order) for assertions.
type fakeAuthorizer struct {
	fn        func(action cedar.Action) (cedar.Decision, error)
	actions   []cedar.Action
	resources []*cedar.Resource
}

func (f *fakeAuthorizer) IsAuthorized(_ context.Context, _ *cedar.Principal, action cedar.Action, r *cedar.Resource, _ cedar.RequestContext) (cedar.Decision, error) {
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
	return &fakeAuthorizer{fn: func(cedar.Action) (cedar.Decision, error) { return cedar.DecisionAllow, nil }}
}

// denyAction denies exactly one action and allows every other.
func denyAction(deny cedar.Action) *fakeAuthorizer {
	return &fakeAuthorizer{fn: func(a cedar.Action) (cedar.Decision, error) {
		if a == deny {
			return cedar.DecisionDeny, nil
		}
		return cedar.DecisionAllow, nil
	}}
}

// engineErr simulates a policy-engine fault (fetch/compile), which the
// handler must surface as CodeInternal, distinct from a plain deny.
func engineErr() *fakeAuthorizer {
	return &fakeAuthorizer{fn: func(cedar.Action) (cedar.Decision, error) {
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
		_, err := h.BatchDelete(authedCtx(tid), BatchDeleteArgs{ObjectIDs: ids(1), Collection: "docs"})
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("policy engine error → internal", func(t *testing.T) {
		h := newHandler(&fakeSubmitter{}, engineErr())
		_, err := h.BatchDelete(authedCtx(tid), BatchDeleteArgs{ObjectIDs: ids(1), Collection: "docs"})
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
			Collection: "docs",
			ObjectIDs:  objs,
			TenantID:   uuid.New(), // must be overwritten by ctx tenant
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
		if md.Collection != "docs" || len(md.ObjectIDs) != 3 || md.ObjectIDs[0] != objs[0] {
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
		_, err := h.BatchCopy(authedCtx(tid), BatchCopyArgs{ObjectIDs: ids(1), SrcCollection: "src", DstCollection: "dst"})
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
		_, err := h.BatchCopy(authedCtx(tid), BatchCopyArgs{ObjectIDs: ids(1), SrcCollection: "src", DstCollection: "dst"})
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
			SrcCollection: "src",
			DstCollection: "dst",
			KeyPrefix:     "archive/",
			ObjectIDs:     objs,
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
		if md.SrcCollection != "src" || md.DstCollection != "dst" || md.KeyPrefix != "archive/" {
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
		_, err := h.BatchUpdateTags(authedCtx(tid), BatchUpdateTagsArgs{ObjectIDs: ids(1), Collection: "docs"})
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("ok enqueues BatchUpdateTags with tags + tenant", func(t *testing.T) {
		opID := uuid.New()
		sub := &fakeSubmitter{id: opID}
		az := allowAll()
		got, err := newHandler(sub, az).BatchUpdateTags(authedCtx(tid), BatchUpdateTagsArgs{
			Collection: "docs",
			ObjectIDs:  ids(1),
			Tags:       map[string]string{"env": "prod"},
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

	t.Run("batch too large → invalid argument", func(t *testing.T) {
		sub := &fakeSubmitter{}
		h := newHandler(sub, allowAll())
		_, err := h.BatchUpdateTags(authedCtx(tid), BatchUpdateTagsArgs{ObjectIDs: make([]uuid.UUID, maxBatchSize+1)})
		wantCode(t, err, connect.CodeInvalidArgument)
		if sub.called {
			t.Fatal("submitter must not be called when batch is oversized")
		}
	})

	t.Run("batch at maxBatchSize → accepted", func(t *testing.T) {
		sub := &fakeSubmitter{id: uuid.New()}
		h := newHandler(sub, allowAll())
		if _, err := h.BatchUpdateTags(authedCtx(tid), BatchUpdateTagsArgs{ObjectIDs: make([]uuid.UUID, maxBatchSize)}); err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if !sub.called {
			t.Fatal("expected submit for a batch at the cap")
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
		_, err := h.BatchRestoreObjects(authedCtx(tid), BatchRestoreObjectsArgs{ObjectIDs: ids(1), Collection: "docs"})
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("ok enqueues BatchRestoreObjects, authorizes RestoreObject", func(t *testing.T) {
		opID := uuid.New()
		sub := &fakeSubmitter{id: opID}
		az := allowAll()
		got, err := newHandler(sub, az).BatchRestoreObjects(authedCtx(tid), BatchRestoreObjectsArgs{
			Collection: "docs",
			ObjectIDs:  ids(4),
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
		if md.TenantID != tid || md.Collection != "docs" || len(md.ObjectIDs) != 4 {
			t.Fatalf("metadata mismatch: %+v", md)
		}
		if len(az.actions) != 1 || az.actions[0] != cedar.ActionRestoreObject {
			t.Fatalf("authorized actions: %v", az.actions)
		}
	})
}

// The submit-time authz Resource must carry the resolved (backend, bucket) for
// EACH target object-key so a bucket:/collection:-scoped PAT enforces — the
// batch worker does not re-check Cedar per object.
func TestBatchAuthzResourceCarriesBucket(t *testing.T) {
	tid := uuid.New()
	az := allowAll()
	buckets := fakeBuckets{byKey: map[string][2]string{
		"src": {"be-s", "bucket-src"},
		"dst": {"be-d", "bucket-dst"},
	}}
	_, err := NewHandler(&fakeSubmitter{id: uuid.New()}, az, buckets, &fakeObjects{}).
		BatchCopy(authedCtx(tid), BatchCopyArgs{ObjectIDs: ids(1), SrcCollection: "src", DstCollection: "dst"})
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

func (permitStore) Fetch(context.Context, uuid.UUID, string) (cedar.Layers, []byte, string, error) {
	return cedar.Layers{Tenant: "permit(principal, action, resource);"}, nil, "", nil
}
func (permitStore) Watch(context.Context) (<-chan cedar.ChangeEvent, error) { return nil, nil }

func scopedCtx(tid uuid.UUID, scopes ...auth.Scope) context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{
		Subject: "svc", TenantID: tid, Scopes: scopes,
	})
}

// End-to-end against the real Cedar engine: a bucket:/collection:-scoped PAT
// may submit a batch confined to its scoped object-key, and is DENIED when any
// target object-key is off-scope (BatchCopy dst).
func TestBatchScopeEnforcedByEngine(t *testing.T) {
	tid := uuid.New()
	engine := cedar.NewEngine(permitStore{}, time.Minute)
	buckets := fakeBuckets{byKey: map[string][2]string{
		"docs":   {"be", "bkt"},
		"secret": {"be", "bkt"},
	}}
	okScope := auth.Scope{Type: auth.ScopeCollection, Value: "bkt/docs"}

	t.Run("allow on scoped object-key", func(t *testing.T) {
		sub := &fakeSubmitter{id: uuid.New()}
		_, err := NewHandler(sub, engine, buckets, &fakeObjects{}).
			BatchDelete(scopedCtx(tid, okScope), BatchDeleteArgs{Collection: "docs", ObjectIDs: ids(1)})
		if err != nil {
			t.Fatalf("scoped PAT on its own object-key must be allowed, got %v", err)
		}
		if !sub.called {
			t.Fatal("expected submit for in-scope batch")
		}
	})

	t.Run("deny off scoped object-key", func(t *testing.T) {
		sub := &fakeSubmitter{id: uuid.New()}
		_, err := NewHandler(sub, engine, buckets, &fakeObjects{}).
			BatchDelete(scopedCtx(tid, okScope), BatchDeleteArgs{Collection: "secret", ObjectIDs: ids(1)})
		wantCode(t, err, connect.CodePermissionDenied)
		if sub.called {
			t.Fatal("submitter must not run for an off-scope batch")
		}
	})

	t.Run("deny whole submit when ANY target off-scope (copy dst)", func(t *testing.T) {
		sub := &fakeSubmitter{id: uuid.New()}
		_, err := NewHandler(sub, engine, buckets, &fakeObjects{}).
			BatchCopy(scopedCtx(tid, okScope), BatchCopyArgs{
				ObjectIDs: ids(1), SrcCollection: "docs", DstCollection: "secret",
			})
		wantCode(t, err, connect.CodePermissionDenied)
		if sub.called {
			t.Fatal("submitter must not run when a copy target is off-scope")
		}
	})
}

// ─── resource-restricted capabilities ──────────────────────────────────────

const (
	scopedCollection = "docs"
	inScopeDir       = "in/"
)

// restrictedCtx carries a capability confined to inScopeDir of
// scopedCollection, with ops.
func restrictedCtx(tid uuid.UUID, ops ...capability.Op) context.Context {
	return auth.WithCapability(authedCtx(tid), &capability.Capability{Caveats: capability.Caveats{
		Ops:              ops,
		ResourcePrefixes: []string{objecth.CapabilityObjectURI(tid, scopedCollection, inScopeDir)},
	}})
}

func object(collection, key string) objecth.Object {
	return objecth.Object{ObjectID: uuid.New(), Collection: collection, Key: key}
}

func idsOf(objs ...objecth.Object) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(objs))
	for _, o := range objs {
		out = append(out, o.ObjectID)
	}
	return out
}

// The three batches over one collection check a restricted capability against
// each object they will act on.
func TestBatchOverOneCollectionChecksEachObject(t *testing.T) {
	tid := uuid.New()
	inside := object(scopedCollection, inScopeDir+"a.txt")
	outside := object(scopedCollection, "out/b.txt")
	elsewhere := object("other", inScopeDir+"c.txt")

	batches := map[string]struct {
		op     capability.Op
		submit func(*Handler, context.Context, []uuid.UUID) error
	}{
		"BatchDelete": {capability.OpDelete, func(h *Handler, ctx context.Context, ids []uuid.UUID) error {
			_, err := h.BatchDelete(ctx, BatchDeleteArgs{Collection: scopedCollection, ObjectIDs: ids})
			return err
		}},
		"BatchUpdateTags": {capability.OpTag, func(h *Handler, ctx context.Context, ids []uuid.UUID) error {
			_, err := h.BatchUpdateTags(ctx, BatchUpdateTagsArgs{Collection: scopedCollection, ObjectIDs: ids})
			return err
		}},
		"BatchRestoreObjects": {capability.OpPut, func(h *Handler, ctx context.Context, ids []uuid.UUID) error {
			_, err := h.BatchRestoreObjects(ctx, BatchRestoreObjectsArgs{Collection: scopedCollection, ObjectIDs: ids})
			return err
		}},
	}
	cases := map[string]struct {
		ids  []uuid.UUID
		code connect.Code // 0: submitted
	}{
		"every object in scope":           {ids: idsOf(inside)},
		"in scope, and one missing":       {ids: append(idsOf(inside), uuid.New())},
		"one object out of scope":         {ids: idsOf(inside, outside), code: connect.CodePermissionDenied},
		"only another collection's":       {ids: idsOf(elsewhere), code: connect.CodePermissionDenied},
		"no object of the batch exists":   {ids: ids(2), code: connect.CodePermissionDenied},
		"another collection's is skipped": {ids: idsOf(inside, elsewhere)},
	}
	for name, b := range batches {
		for cname, tc := range cases {
			t.Run(name+"/"+cname, func(t *testing.T) {
				sub := &fakeSubmitter{id: uuid.New()}
				h := NewHandler(sub, allowAll(), fakeBuckets{}, &fakeObjects{objs: []objecth.Object{inside, outside, elsewhere}})
				err := b.submit(h, restrictedCtx(tid, b.op), tc.ids)
				if tc.code == 0 {
					if err != nil || !sub.called {
						t.Fatalf("err = %v, submitted = %v; want submitted", err, sub.called)
					}
					return
				}
				wantCode(t, err, tc.code)
				if sub.called {
					t.Fatal("a refused batch was submitted")
				}
			})
		}
	}
}

// Without resource caveats the op alone decides, as before, and no object is
// read for it; nor for a caller with no capability at all.
func TestBatchReadsObjectsOnlyForARestrictedCapability(t *testing.T) {
	tid := uuid.New()
	for name, ctx := range map[string]context.Context{
		"unrestricted capability": authedCtxWithCap(tid, capability.OpDelete),
		"no capability":           authedCtx(tid),
	} {
		t.Run(name, func(t *testing.T) {
			objs := &fakeObjects{}
			sub := &fakeSubmitter{id: uuid.New()}
			if _, err := NewHandler(sub, allowAll(), fakeBuckets{}, objs).
				BatchDelete(ctx, BatchDeleteArgs{Collection: scopedCollection, ObjectIDs: ids(1)}); err != nil {
				t.Fatalf("BatchDelete: %v", err)
			}
			if objs.calls != 0 {
				t.Errorf("objects read %d times", objs.calls)
			}
		})
	}
}

func TestBatchObjectLookupFailureRefuses(t *testing.T) {
	tid := uuid.New()
	sub := &fakeSubmitter{}
	_, err := NewHandler(sub, allowAll(), fakeBuckets{}, &fakeObjects{err: errors.New("db down")}).
		BatchDelete(restrictedCtx(tid, capability.OpDelete), BatchDeleteArgs{Collection: scopedCollection, ObjectIDs: ids(1)})
	if err == nil || sub.called {
		t.Fatalf("err = %v, submitted = %v; want refused", err, sub.called)
	}
}

// A copy reads its sources and writes its destinations: get on each source,
// put on each KeyPrefix + key under the destination collection.
func TestBatchCopyChecksSourcesAndDestinations(t *testing.T) {
	tid := uuid.New()
	src := object(scopedCollection, inScopeDir+"a.txt")
	tainted := object(scopedCollection, inScopeDir+"t.txt")
	tainted.Taint = []string{"pii"}
	readWrite := []capability.Op{capability.OpGet, capability.OpPut}

	cases := map[string]struct {
		ctx       context.Context
		dst       string
		keyPrefix string
		ids       []uuid.UUID
		code      connect.Code
	}{
		"restricted, both ends in scope": {
			ctx: restrictedCtx(tid, readWrite...), dst: scopedCollection, keyPrefix: inScopeDir + "copy/", ids: idsOf(src),
		},
		"restricted, destination out of scope": {
			ctx: restrictedCtx(tid, readWrite...), dst: scopedCollection, keyPrefix: "out/", ids: idsOf(src),
			code: connect.CodePermissionDenied,
		},
		"restricted, no source exists": {
			ctx: restrictedCtx(tid, readWrite...), dst: scopedCollection, keyPrefix: inScopeDir, ids: ids(1),
			code: connect.CodePermissionDenied,
		},
		"unrestricted without get": {
			ctx: authedCtxWithCap(tid, capability.OpPut), dst: "dst", ids: idsOf(src), code: connect.CodePermissionDenied,
		},
		"unrestricted, a tainted source": {
			ctx: authedCtxWithCap(tid, readWrite...), dst: "dst", ids: idsOf(tainted), code: connect.CodePermissionDenied,
		},
		"unrestricted, allowed tainted reads": {
			ctx: auth.WithCapability(authedCtx(tid), &capability.Capability{Caveats: capability.Caveats{
				Ops: readWrite, AllowTaintedRead: true,
			}}),
			dst: "dst", ids: idsOf(tainted),
		},
		"unrestricted, no source exists": {
			ctx: authedCtxWithCap(tid, readWrite...), dst: "dst", ids: ids(1),
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			sub := &fakeSubmitter{id: uuid.New()}
			h := NewHandler(sub, allowAll(), fakeBuckets{}, &fakeObjects{objs: []objecth.Object{src, tainted}})
			_, err := h.BatchCopy(tc.ctx, BatchCopyArgs{
				SrcCollection: scopedCollection, DstCollection: tc.dst, KeyPrefix: tc.keyPrefix, ObjectIDs: tc.ids,
			})
			if tc.code == 0 {
				if err != nil || !sub.called {
					t.Fatalf("err = %v, submitted = %v; want submitted", err, sub.called)
				}
				return
			}
			wantCode(t, err, tc.code)
			if sub.called {
				t.Fatal("a refused copy was submitted")
			}
		})
	}
}
