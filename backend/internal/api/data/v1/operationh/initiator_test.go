package operationh

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
)

// An operation records who started it: the subject of the submitting token.
func TestSubmit_RecordsTheInitiator(t *testing.T) {
	var got Operation
	fr := &fakeRepo{createFn: func(_ context.Context, op Operation) error {
		got = op
		return nil
	}}
	if _, err := NewHandler(fr, allowAuthorizer()).Submit(authedCtx(uuid.New()), "BatchDelete", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	if got.InitiatorSubject != "u1" {
		t.Errorf("initiator = %q, want the caller's subject", got.InitiatorSubject)
	}
}

// mine narrows the list to the caller's own operations, in the query; without
// it the tenant's whole list comes, as before.
func TestListOperations_Mine(t *testing.T) {
	tid := uuid.New()
	var initiator string
	calls := 0
	fr := &fakeRepo{listFn: func(_ context.Context, _ uuid.UUID, _ *State, _ uuid.UUID, _ int32, _ string, _ bool, i string) ([]Operation, string, error) {
		calls++
		initiator = i
		return nil, "", nil
	}}
	h := NewHandler(fr, allowAuthorizer())

	if _, _, err := h.ListOperations(authedCtx(tid), nil, 10, "", "", true, true); err != nil {
		t.Fatal(err)
	}
	if initiator != "u1" {
		t.Errorf("mine: store asked for %q's operations, want u1's", initiator)
	}
	if _, _, err := h.ListOperations(authedCtx(tid), nil, 10, "", "", true, false); err != nil {
		t.Fatal(err)
	}
	if initiator != "" {
		t.Errorf("not mine: store narrowed to %q, want the whole tenant", initiator)
	}

	// A caller with no subject owns nothing: it is refused, not shown everyone's.
	calls = 0
	nameless := auth.WithPrincipal(context.Background(), &auth.Principal{TenantID: tid})
	_, _, err := h.ListOperations(nameless, nil, 10, "", "", true, true)
	if connect.CodeOf(err) != connect.CodeUnauthenticated || calls != 0 {
		t.Errorf("no subject: code %v, store calls %d; want Unauthenticated and none", connect.CodeOf(err), calls)
	}
}
