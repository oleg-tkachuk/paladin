package batch

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

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

// fakeAuthorizer is a configurable cedar.Authorizer. fn decides the
// verdict per action so BatchCopy (two authorize calls) can allow the
// source check and deny the destination one. It records the actions in
// call order for assertions.
type fakeAuthorizer struct {
	fn      func(action string) (cedar.Decision, error)
	actions []string
}

func (f *fakeAuthorizer) IsAuthorized(_ context.Context, _ *cedar.Principal, action string, _ *cedar.Resource, _ cedar.RequestContext) (cedar.Decision, error) {
	f.actions = append(f.actions, action)
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
		h := NewHandler(&fakeSubmitter{}, allowAll())
		_, err := h.BatchDelete(context.Background(), BatchDeleteArgs{ObjectIDs: ids(1)})
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("principal without tenant → unauthenticated", func(t *testing.T) {
		ctx := auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u1"})
		h := NewHandler(&fakeSubmitter{}, allowAll())
		_, err := h.BatchDelete(ctx, BatchDeleteArgs{ObjectIDs: ids(1)})
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("empty object_ids → invalid argument", func(t *testing.T) {
		h := NewHandler(&fakeSubmitter{}, allowAll())
		_, err := h.BatchDelete(authedCtx(tid), BatchDeleteArgs{})
		wantCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("batch too large → invalid argument", func(t *testing.T) {
		sub := &fakeSubmitter{}
		h := NewHandler(sub, allowAll())
		_, err := h.BatchDelete(authedCtx(tid), BatchDeleteArgs{ObjectIDs: make([]uuid.UUID, maxBatchSize+1)})
		wantCode(t, err, connect.CodeInvalidArgument)
		if sub.called {
			t.Fatal("submitter must not be called when batch is oversized")
		}
	})

	t.Run("capability lacks delete op → permission denied", func(t *testing.T) {
		ctx := authedCtxWithCap(tid, capability.OpGet) // no OpDelete
		h := NewHandler(&fakeSubmitter{}, allowAll())
		_, err := h.BatchDelete(ctx, BatchDeleteArgs{ObjectIDs: ids(1)})
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("policy denies → permission denied", func(t *testing.T) {
		h := NewHandler(&fakeSubmitter{}, denyAction(cedar.ActionDeleteObject))
		_, err := h.BatchDelete(authedCtx(tid), BatchDeleteArgs{ObjectIDs: ids(1), ObjectKey: "docs"})
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("policy engine error → internal", func(t *testing.T) {
		h := NewHandler(&fakeSubmitter{}, engineErr())
		_, err := h.BatchDelete(authedCtx(tid), BatchDeleteArgs{ObjectIDs: ids(1), ObjectKey: "docs"})
		wantCode(t, err, connect.CodeInternal)
	})

	t.Run("submitter error propagates", func(t *testing.T) {
		sub := &fakeSubmitter{err: connect.NewError(connect.CodeUnavailable, errors.New("db down"))}
		h := NewHandler(sub, allowAll())
		_, err := h.BatchDelete(authedCtx(tid), BatchDeleteArgs{ObjectIDs: ids(1)})
		wantCode(t, err, connect.CodeUnavailable)
	})

	t.Run("ok stamps tenant, enqueues BatchDelete, authorizes DeleteObject", func(t *testing.T) {
		opID := uuid.New()
		sub := &fakeSubmitter{id: opID}
		az := allowAll()
		objs := ids(3)
		got, err := NewHandler(sub, az).BatchDelete(authedCtx(tid), BatchDeleteArgs{
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
		_, err := NewHandler(sub, allowAll()).BatchDelete(ctx, BatchDeleteArgs{ObjectIDs: ids(1)})
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
		h := NewHandler(&fakeSubmitter{}, allowAll())
		_, err := h.BatchCopy(context.Background(), BatchCopyArgs{ObjectIDs: ids(1)})
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("empty object_ids → invalid argument", func(t *testing.T) {
		h := NewHandler(&fakeSubmitter{}, allowAll())
		_, err := h.BatchCopy(authedCtx(tid), BatchCopyArgs{})
		wantCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("batch too large → invalid argument", func(t *testing.T) {
		h := NewHandler(&fakeSubmitter{}, allowAll())
		_, err := h.BatchCopy(authedCtx(tid), BatchCopyArgs{ObjectIDs: make([]uuid.UUID, maxBatchSize+1)})
		wantCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("capability lacks put op → permission denied", func(t *testing.T) {
		ctx := authedCtxWithCap(tid, capability.OpGet) // no OpPut
		h := NewHandler(&fakeSubmitter{}, allowAll())
		_, err := h.BatchCopy(ctx, BatchCopyArgs{ObjectIDs: ids(1)})
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("source copy denied → permission denied", func(t *testing.T) {
		az := denyAction(cedar.ActionCopyObject)
		h := NewHandler(&fakeSubmitter{}, az)
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
		h := NewHandler(sub, az)
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
		got, err := NewHandler(sub, allowAll()).BatchCopy(authedCtx(tid), BatchCopyArgs{
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
		h := NewHandler(&fakeSubmitter{}, allowAll())
		_, err := h.BatchUpdateTags(context.Background(), BatchUpdateTagsArgs{ObjectIDs: ids(1)})
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("empty object_ids → invalid argument", func(t *testing.T) {
		h := NewHandler(&fakeSubmitter{}, allowAll())
		_, err := h.BatchUpdateTags(authedCtx(tid), BatchUpdateTagsArgs{Tags: map[string]string{"k": "v"}})
		wantCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("capability lacks tag op → permission denied", func(t *testing.T) {
		ctx := authedCtxWithCap(tid, capability.OpGet) // no OpTag
		h := NewHandler(&fakeSubmitter{}, allowAll())
		_, err := h.BatchUpdateTags(ctx, BatchUpdateTagsArgs{ObjectIDs: ids(1)})
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("policy denies UpdateObject → permission denied", func(t *testing.T) {
		h := NewHandler(&fakeSubmitter{}, denyAction(cedar.ActionUpdateObject))
		_, err := h.BatchUpdateTags(authedCtx(tid), BatchUpdateTagsArgs{ObjectIDs: ids(1), ObjectKey: "docs"})
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("ok enqueues BatchUpdateTags with tags + tenant", func(t *testing.T) {
		opID := uuid.New()
		sub := &fakeSubmitter{id: opID}
		az := allowAll()
		got, err := NewHandler(sub, az).BatchUpdateTags(authedCtx(tid), BatchUpdateTagsArgs{
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
		_, err := NewHandler(sub, allowAll()).BatchUpdateTags(authedCtx(tid), BatchUpdateTagsArgs{
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
		h := NewHandler(&fakeSubmitter{}, allowAll())
		_, err := h.BatchRestoreObjects(context.Background(), BatchRestoreObjectsArgs{ObjectIDs: ids(1)})
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("empty object_ids → invalid argument", func(t *testing.T) {
		h := NewHandler(&fakeSubmitter{}, allowAll())
		_, err := h.BatchRestoreObjects(authedCtx(tid), BatchRestoreObjectsArgs{})
		wantCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("batch too large → invalid argument", func(t *testing.T) {
		h := NewHandler(&fakeSubmitter{}, allowAll())
		_, err := h.BatchRestoreObjects(authedCtx(tid), BatchRestoreObjectsArgs{ObjectIDs: make([]uuid.UUID, maxBatchSize+1)})
		wantCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("capability lacks put op → permission denied", func(t *testing.T) {
		ctx := authedCtxWithCap(tid, capability.OpGet) // restore requires OpPut
		h := NewHandler(&fakeSubmitter{}, allowAll())
		_, err := h.BatchRestoreObjects(ctx, BatchRestoreObjectsArgs{ObjectIDs: ids(1)})
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("policy denies RestoreObject → permission denied", func(t *testing.T) {
		h := NewHandler(&fakeSubmitter{}, denyAction(cedar.ActionRestoreObject))
		_, err := h.BatchRestoreObjects(authedCtx(tid), BatchRestoreObjectsArgs{ObjectIDs: ids(1), ObjectKey: "docs"})
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("ok enqueues BatchRestoreObjects, authorizes RestoreObject", func(t *testing.T) {
		opID := uuid.New()
		sub := &fakeSubmitter{id: opID}
		az := allowAll()
		got, err := NewHandler(sub, az).BatchRestoreObjects(authedCtx(tid), BatchRestoreObjectsArgs{
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
