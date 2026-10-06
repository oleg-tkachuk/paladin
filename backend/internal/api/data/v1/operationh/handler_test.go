package operationh

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
)

// fakeRepo is a configurable in-memory Repository. It records the args the
// handler forwards so tests can assert the tenant-stamping and error-mapping
// contract without a database. Methods the handler under test never calls
// (UpdateState, ClaimNext) return zero values to satisfy the interface.
type fakeRepo struct {
	createFn func(ctx context.Context, op Operation) error
	getFn    func(ctx context.Context, opID, tenantID uuid.UUID) (Operation, error)
	cancelFn func(ctx context.Context, opID, tenantID uuid.UUID) error
	listFn   func(ctx context.Context, tenantID uuid.UUID, state *State, afterID uuid.UUID, pageSize int32, filter string, newestFirst bool) ([]Operation, string, error)

	lastCreate Operation
	lastGet    struct{ opID, tenantID uuid.UUID }
	lastCancel struct{ opID, tenantID uuid.UUID }
	lastList   struct {
		tenantID    uuid.UUID
		state       *State
		afterID     uuid.UUID
		pageSize    int32
		filter      string
		newestFirst bool
	}
}

func (f *fakeRepo) Create(ctx context.Context, op Operation) error {
	f.lastCreate = op
	if f.createFn != nil {
		return f.createFn(ctx, op)
	}
	return nil
}

func (f *fakeRepo) Get(ctx context.Context, opID, tenantID uuid.UUID) (Operation, error) {
	f.lastGet.opID, f.lastGet.tenantID = opID, tenantID
	return f.getFn(ctx, opID, tenantID)
}

func (f *fakeRepo) UpdateState(context.Context, uuid.UUID, State, []byte, []byte, string, string) error {
	return nil
}

func (f *fakeRepo) Cancel(ctx context.Context, opID, tenantID uuid.UUID) error {
	f.lastCancel.opID, f.lastCancel.tenantID = opID, tenantID
	return f.cancelFn(ctx, opID, tenantID)
}

func (f *fakeRepo) List(ctx context.Context, tenantID uuid.UUID, state *State, afterID uuid.UUID, pageSize int32, filter string, newestFirst bool) ([]Operation, string, error) {
	f.lastList.tenantID = tenantID
	f.lastList.state = state
	f.lastList.afterID = afterID
	f.lastList.pageSize = pageSize
	f.lastList.filter = filter
	f.lastList.newestFirst = newestFirst
	return f.listFn(ctx, tenantID, state, afterID, pageSize, filter, newestFirst)
}

func (f *fakeRepo) ClaimNext(context.Context) (Operation, error) {
	return Operation{}, ErrNoOperationToClaim
}

// authzCall captures what the handler forwarded to the Cedar authorizer so
// tests can assert the action name and the resource tenant that gate each RPC.
type authzCall struct {
	action    cedar.Action
	principal cedar.Principal
	resource  cedar.Resource
}

// recordingAuthorizer is a cedar.Authorizer fake returning a fixed decision
// (and optional engine error) while recording every call.
type recordingAuthorizer struct {
	decision cedar.Decision
	err      error
	calls    []authzCall
}

func (a *recordingAuthorizer) IsAuthorized(_ context.Context, p *cedar.Principal, action cedar.Action, r *cedar.Resource, _ cedar.RequestContext) (cedar.Decision, error) {
	a.calls = append(a.calls, authzCall{action: action, principal: *p, resource: *r})
	return a.decision, a.err
}

func allowAuthorizer() *recordingAuthorizer {
	return &recordingAuthorizer{decision: cedar.DecisionAllow}
}
func denyAuthorizer() *recordingAuthorizer {
	return &recordingAuthorizer{decision: cedar.DecisionDeny}
}
func errAuthorizer() *recordingAuthorizer {
	return &recordingAuthorizer{decision: cedar.DecisionDeny, err: errors.New("engine boom")}
}

func authedCtx(tid uuid.UUID) context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u1", TenantID: tid})
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

func TestNewHandler(t *testing.T) {
	t.Run("nil policy panics", func(t *testing.T) {
		defer func() {
			if recover() == nil {
				t.Fatalf("expected panic on nil policy authorizer")
			}
		}()
		_ = NewHandler(&fakeRepo{}, nil)
	})

	t.Run("non-nil policy constructs", func(t *testing.T) {
		if NewHandler(&fakeRepo{}, allowAuthorizer()) == nil {
			t.Fatalf("expected non-nil handler")
		}
	})
}

func TestGetOperation(t *testing.T) {
	tid := uuid.New()
	opID := uuid.New()

	t.Run("unauthenticated", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, allowAuthorizer())
		_, err := h.GetOperation(context.Background(), opID)
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("principal without tenant → unauthenticated", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, allowAuthorizer())
		ctx := auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u1"}) // TenantID zero
		_, err := h.GetOperation(ctx, opID)
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("policy denies → permission denied", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, denyAuthorizer())
		_, err := h.GetOperation(authedCtx(tid), opID)
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("policy engine error → internal", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, errAuthorizer())
		_, err := h.GetOperation(authedCtx(tid), opID)
		wantCode(t, err, connect.CodeInternal)
	})

	t.Run("repo error → not found", func(t *testing.T) {
		h := NewHandler(&fakeRepo{getFn: func(context.Context, uuid.UUID, uuid.UUID) (Operation, error) {
			return Operation{}, errors.New("missing")
		}}, allowAuthorizer())
		_, err := h.GetOperation(authedCtx(tid), opID)
		wantCode(t, err, connect.CodeNotFound)
	})

	t.Run("ok forwards opID+tenant and authorizes with ReadOperation", func(t *testing.T) {
		fr := &fakeRepo{getFn: func(_ context.Context, id, tenant uuid.UUID) (Operation, error) {
			return Operation{OperationID: id, TenantID: tenant, State: StateSucceeded}, nil
		}}
		az := allowAuthorizer()
		got, err := NewHandler(fr, az).GetOperation(authedCtx(tid), opID)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if fr.lastGet.opID != opID || fr.lastGet.tenantID != tid {
			t.Fatalf("forwarded (%v,%v), want (%v,%v)", fr.lastGet.opID, fr.lastGet.tenantID, opID, tid)
		}
		if got.OperationID != opID || got.State != StateSucceeded {
			t.Fatalf("returned op %+v", got)
		}
		if len(az.calls) != 1 {
			t.Fatalf("authorizer called %d times, want 1", len(az.calls))
		}
		if az.calls[0].action != cedar.ActionReadOperation {
			t.Fatalf("action: got %q want %q", az.calls[0].action, cedar.ActionReadOperation)
		}
		if az.calls[0].resource.TenantID != tid || az.calls[0].principal.Subject != "u1" {
			t.Fatalf("authz forwarded principal=%+v resource=%+v", az.calls[0].principal, az.calls[0].resource)
		}
	})
}

func TestCancelOperation(t *testing.T) {
	tid := uuid.New()
	opID := uuid.New()

	t.Run("unauthenticated", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, allowAuthorizer())
		err := h.CancelOperation(context.Background(), opID)
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("policy denies → permission denied", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, denyAuthorizer())
		err := h.CancelOperation(authedCtx(tid), opID)
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("repo not-cancellable → failed precondition", func(t *testing.T) {
		h := NewHandler(&fakeRepo{cancelFn: func(context.Context, uuid.UUID, uuid.UUID) error {
			return ErrNotCancellable
		}}, allowAuthorizer())
		err := h.CancelOperation(authedCtx(tid), opID)
		wantCode(t, err, connect.CodeFailedPrecondition)
	})

	t.Run("ok forwards args and authorizes with CancelOperation", func(t *testing.T) {
		fr := &fakeRepo{cancelFn: func(context.Context, uuid.UUID, uuid.UUID) error { return nil }}
		az := allowAuthorizer()
		if err := NewHandler(fr, az).CancelOperation(authedCtx(tid), opID); err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if fr.lastCancel.opID != opID || fr.lastCancel.tenantID != tid {
			t.Fatalf("forwarded %+v, want (%v,%v)", fr.lastCancel, opID, tid)
		}
		if len(az.calls) != 1 || az.calls[0].action != cedar.ActionCancelOperation {
			t.Fatalf("authz calls=%+v", az.calls)
		}
	})
}

func TestListOperations(t *testing.T) {
	tid := uuid.New()

	t.Run("unauthenticated", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, allowAuthorizer())
		_, _, err := h.ListOperations(context.Background(), nil, 10, "", "", false)
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("policy denies → permission denied", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, denyAuthorizer())
		_, _, err := h.ListOperations(authedCtx(tid), nil, 10, "", "", false)
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("invalid page token → invalid argument", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, allowAuthorizer())
		_, _, err := h.ListOperations(authedCtx(tid), nil, 10, "not-a-uuid", "", false)
		wantCode(t, err, connect.CodeInvalidArgument)
	})

	t.Run("empty token → nil afterID and passthrough result", func(t *testing.T) {
		state := StateRunning
		fr := &fakeRepo{listFn: func(_ context.Context, tenant uuid.UUID, _ *State, _ uuid.UUID, _ int32, _ string, _ bool) ([]Operation, string, error) {
			return []Operation{{TenantID: tenant, State: StateRunning}}, "next-tok", nil
		}}
		ops, next, err := NewHandler(fr, allowAuthorizer()).ListOperations(authedCtx(tid), &state, 25, "", "", false)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if fr.lastList.afterID != uuid.Nil {
			t.Fatalf("afterID: got %v want nil for empty token", fr.lastList.afterID)
		}
		if fr.lastList.state == nil || *fr.lastList.state != StateRunning {
			t.Fatalf("state not forwarded: %+v", fr.lastList.state)
		}
		if fr.lastList.tenantID != tid || fr.lastList.pageSize != 25 {
			t.Fatalf("forwarded tenant=%v pageSize=%d", fr.lastList.tenantID, fr.lastList.pageSize)
		}
		if len(ops) != 1 || next != "next-tok" {
			t.Fatalf("got ops=%v next=%q", ops, next)
		}
	})

	t.Run("valid token parsed into afterID", func(t *testing.T) {
		after := uuid.New()
		fr := &fakeRepo{listFn: func(context.Context, uuid.UUID, *State, uuid.UUID, int32, string, bool) ([]Operation, string, error) {
			return nil, "", nil
		}}
		_, _, err := NewHandler(fr, allowAuthorizer()).ListOperations(authedCtx(tid), nil, 10, after.String(), "", false)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if fr.lastList.afterID != after {
			t.Fatalf("afterID: got %v want %v", fr.lastList.afterID, after)
		}
	})

	t.Run("repo error propagates unwrapped", func(t *testing.T) {
		sentinel := errors.New("list boom")
		fr := &fakeRepo{listFn: func(context.Context, uuid.UUID, *State, uuid.UUID, int32, string, bool) ([]Operation, string, error) {
			return nil, "", sentinel
		}}
		_, _, err := NewHandler(fr, allowAuthorizer()).ListOperations(authedCtx(tid), nil, 10, "", "", false)
		if !errors.Is(err, sentinel) {
			t.Fatalf("expected sentinel error passthrough, got %v", err)
		}
	})
}

func TestSubmit(t *testing.T) {
	tid := uuid.New()

	t.Run("unauthenticated", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, allowAuthorizer())
		_, err := h.Submit(context.Background(), "BatchDelete", nil)
		wantCode(t, err, connect.CodeUnauthenticated)
	})

	t.Run("repo error → internal", func(t *testing.T) {
		h := NewHandler(&fakeRepo{createFn: func(context.Context, Operation) error {
			return errors.New("insert failed")
		}}, allowAuthorizer())
		id, err := h.Submit(authedCtx(tid), "BatchDelete", nil)
		wantCode(t, err, connect.CodeInternal)
		if id != uuid.Nil {
			t.Fatalf("expected Nil id on error, got %v", id)
		}
	})

	t.Run("ok stamps tenant, pending state, type, metadata and returns generated id", func(t *testing.T) {
		meta := []byte(`{"targets":3}`)
		fr := &fakeRepo{}
		id, err := NewHandler(fr, allowAuthorizer()).Submit(authedCtx(tid), "BatchCopy", meta)
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if id == uuid.Nil {
			t.Fatalf("expected generated operation id, got Nil")
		}
		if fr.lastCreate.OperationID != id {
			t.Fatalf("returned id %v != persisted id %v", id, fr.lastCreate.OperationID)
		}
		if fr.lastCreate.TenantID != tid {
			t.Fatalf("tenant not stamped: got %v want %v", fr.lastCreate.TenantID, tid)
		}
		if fr.lastCreate.State != StatePending {
			t.Fatalf("state: got %q want %q", fr.lastCreate.State, StatePending)
		}
		if fr.lastCreate.Type != "BatchCopy" {
			t.Fatalf("type: got %q want BatchCopy", fr.lastCreate.Type)
		}
		if string(fr.lastCreate.Metadata) != string(meta) {
			t.Fatalf("metadata: got %q want %q", fr.lastCreate.Metadata, meta)
		}
	})
}
