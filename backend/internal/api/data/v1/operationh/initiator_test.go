package operationh

import (
	"context"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"

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
	fr := &fakeRepo{listFn: func(_ context.Context, args ListArgs) ([]Operation, string, error) {
		calls++
		initiator = args.Initiator
		return nil, "", nil
	}}
	h := NewHandler(fr, allowAuthorizer())

	if _, _, err := h.ListOperations(authedCtx(tid), ListInput{PageSize: 10, NewestFirst: true, Mine: true}); err != nil {
		t.Fatal(err)
	}
	if initiator != "u1" {
		t.Errorf("mine: store asked for %q's operations, want u1's", initiator)
	}
	if _, _, err := h.ListOperations(authedCtx(tid), ListInput{PageSize: 10, NewestFirst: true}); err != nil {
		t.Fatal(err)
	}
	if initiator != "" {
		t.Errorf("not mine: store narrowed to %q, want the whole tenant", initiator)
	}

	// A caller with no subject owns nothing: it is refused, not shown everyone's.
	calls = 0
	nameless := auth.WithPrincipal(context.Background(), &auth.Principal{TenantID: tid})
	_, _, err := h.ListOperations(nameless, ListInput{PageSize: 10, NewestFirst: true, Mine: true})
	if connect.CodeOf(err) != connect.CodeUnauthenticated || calls != 0 {
		t.Errorf("no subject: code %v, store calls %d; want Unauthenticated and none", connect.CodeOf(err), calls)
	}
}

// Dismissing clears the caller's own finished operations from their list,
// and only theirs: the store is asked with the caller's subject.
func TestDismissOperations(t *testing.T) {
	tid := uuid.New()
	ids := []uuid.UUID{uuid.New()}
	var gotTenant uuid.UUID
	var gotSubject string
	var gotIDs []uuid.UUID
	fr := &fakeRepo{dismissFn: func(_ context.Context, tenant uuid.UUID, subject string, i []uuid.UUID) (int64, error) {
		gotTenant, gotSubject, gotIDs = tenant, subject, i
		return 1, nil
	}}
	n, err := NewHandler(fr, allowAuthorizer()).DismissOperations(authedCtx(tid), ids)
	if err != nil || n != 1 {
		t.Fatalf("dismissed %d, %v", n, err)
	}
	if gotTenant != tid || gotSubject != "u1" || len(gotIDs) != 1 || gotIDs[0] != ids[0] {
		t.Errorf("store asked for tenant %s, subject %q, ids %v", gotTenant, gotSubject, gotIDs)
	}

	nameless := auth.WithPrincipal(context.Background(), &auth.Principal{TenantID: tid})
	if _, err := NewHandler(fr, allowAuthorizer()).DismissOperations(nameless, nil); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("no subject: code %v, want Unauthenticated", connect.CodeOf(err))
	}
	if _, err := NewHandler(fr, denyAuthorizer()).DismissOperations(authedCtx(tid), nil); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("denied: code %v, want PermissionDenied", connect.CodeOf(err))
	}
}

// The caller's own list leaves out what they dismissed unless asked; the
// tenant's list is not theirs to narrow.
func TestListOperations_HidesDismissedFromMine(t *testing.T) {
	var hide bool
	fr := &fakeRepo{listFn: func(_ context.Context, args ListArgs) ([]Operation, string, error) {
		hide = args.HideDismissed
		return nil, "", nil
	}}
	h := NewHandler(fr, allowAuthorizer())
	for _, tc := range []struct {
		in   ListInput
		want bool
	}{
		{ListInput{Mine: true}, true},
		{ListInput{Mine: true, IncludeDismissed: true}, false},
		{ListInput{}, false},
		{ListInput{IncludeDismissed: true}, false},
	} {
		if _, _, err := h.ListOperations(authedCtx(uuid.New()), tc.in); err != nil {
			t.Fatal(err)
		}
		if hide != tc.want {
			t.Errorf("%+v: hide dismissed = %v, want %v", tc.in, hide, tc.want)
		}
	}
}
