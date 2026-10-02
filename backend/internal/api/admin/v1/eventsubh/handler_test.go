package eventsubh

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
)

// ─── Test doubles ────────────────────────────────────────────────────────────

type allowAuthorizer struct{}

func (allowAuthorizer) IsAuthorized(context.Context, *cedar.Principal, string, *cedar.Resource, cedar.RequestContext) (cedar.Decision, error) {
	return cedar.DecisionAllow, nil
}

type denyAuthorizer struct{}

func (denyAuthorizer) IsAuthorized(context.Context, *cedar.Principal, string, *cedar.Resource, cedar.RequestContext) (cedar.Decision, error) {
	return cedar.DecisionDeny, nil
}

// fakeRepo implements admindomain.EventSubscriptionRepository. `sub` is the row
// Get returns; the per-method errors drive the error-mapping assertions.
type fakeRepo struct {
	sub       admindomain.EventSubscription
	getErr    error
	createErr error
	updateErr error
	deleteErr error
	stampedID uuid.UUID
	listArgs  admindomain.ListEventSubscriptionsArgs
	listCalls int
	// requeued is what RequeueFailedDeliveries reports; requeueCalls counts
	// the calls, so a refused redrive can be shown not to have reached it.
	requeued     int64
	requeueErr   error
	requeueCalls int
}

func (f *fakeRepo) Create(_ context.Context, s *admindomain.EventSubscription) error {
	if f.createErr != nil {
		return f.createErr
	}
	// Pointer receiver is load-bearing — the handler reads s.SubscriptionID
	// immediately after to re-fetch the row.
	s.SubscriptionID = f.stampedID
	f.sub = *s
	return nil
}
func (f *fakeRepo) Get(context.Context, uuid.UUID) (admindomain.EventSubscription, error) {
	return f.sub, f.getErr
}
func (f *fakeRepo) List(_ context.Context, args admindomain.ListEventSubscriptionsArgs) ([]admindomain.EventSubscription, string, error) {
	f.listArgs = args
	f.listCalls++
	return nil, "", nil
}
func (f *fakeRepo) Update(context.Context, admindomain.EventSubscription, int64, []string) error {
	return f.updateErr
}
func (f *fakeRepo) Delete(context.Context, uuid.UUID, int64) error { return f.deleteErr }
func (f *fakeRepo) RequeueFailedDeliveries(context.Context, uuid.UUID) (int64, error) {
	f.requeueCalls++
	return f.requeued, f.requeueErr
}

type okDispatcher struct{}

func (okDispatcher) DeliverOne(context.Context, admindomain.EventSubscription, string) error {
	return nil
}

type failDispatcher struct{}

func (failDispatcher) DeliverOne(context.Context, admindomain.EventSubscription, string) error {
	return errors.New("connection refused")
}

func ctxAs(tenant uuid.UUID, roles ...string) context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{
		Subject:  "tester",
		TenantID: tenant,
		Roles:    roles,
	})
}

func code(err error) connect.Code { return connect.CodeOf(err) }

// ─── Create ──────────────────────────────────────────────────────────────────

func TestCreate_RoleGate(t *testing.T) {
	h := NewHandler(&fakeRepo{}, allowAuthorizer{})
	_, err := h.Create(ctxAs(uuid.New(), apiutil.RoleTenantUser),
		admindomain.EventSubscription{TenantID: uuid.New()})
	if code(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", code(err))
	}
}

func TestCreate_CrossTenantDenied(t *testing.T) {
	caller := uuid.New()
	h := NewHandler(&fakeRepo{}, allowAuthorizer{})
	_, err := h.Create(ctxAs(caller, apiutil.RoleTenantAdmin),
		admindomain.EventSubscription{TenantID: uuid.New()}) // different tenant
	if code(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied", code(err))
	}
}

func TestCreate_Success_StampsAndRefetches(t *testing.T) {
	caller := uuid.New()
	id := uuid.New()
	repo := &fakeRepo{stampedID: id}
	h := NewHandler(repo, allowAuthorizer{})
	got, err := h.Create(ctxAs(caller, apiutil.RoleTenantAdmin),
		admindomain.EventSubscription{TenantID: caller, SinkKind: "http"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.SubscriptionID != id {
		t.Errorf("returned ID = %s, want the stamped %s", got.SubscriptionID, id)
	}
	if got.SinkKind != "http" {
		t.Errorf("returned sink = %q, want http", got.SinkKind)
	}
}

func TestCreate_CedarDenied(t *testing.T) {
	caller := uuid.New()
	h := NewHandler(&fakeRepo{}, denyAuthorizer{})
	_, err := h.Create(ctxAs(caller, apiutil.RoleTenantAdmin),
		admindomain.EventSubscription{TenantID: caller})
	if code(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied (cedar)", code(err))
	}
}

func TestCreate_InvalidFilterRejected(t *testing.T) {
	caller := uuid.New()
	repo := &fakeRepo{stampedID: uuid.New()}
	h := NewHandler(repo, allowAuthorizer{})
	// `bogus_field` isn't declared on EventEnvelopeSchema → compile error.
	_, err := h.Create(ctxAs(caller, apiutil.RoleTenantAdmin),
		admindomain.EventSubscription{TenantID: caller, SinkKind: "http", CELFilter: "bogus_field == 1"})
	if code(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument for a malformed filter", code(err))
	}
	if repo.sub.SinkKind != "" {
		t.Error("repo.Create must not run when the filter is rejected")
	}
}

func TestCreate_ValidFilterAccepted(t *testing.T) {
	caller := uuid.New()
	repo := &fakeRepo{stampedID: uuid.New()}
	h := NewHandler(repo, allowAuthorizer{})
	_, err := h.Create(ctxAs(caller, apiutil.RoleTenantAdmin),
		admindomain.EventSubscription{TenantID: caller, SinkKind: "http", CELFilter: `type == "paladin.object.uploaded"`})
	if err != nil {
		t.Fatalf("valid CEL filter must be accepted, got: %v", err)
	}
}

// ─── Get ─────────────────────────────────────────────────────────────────────

func TestGet_NotFound(t *testing.T) {
	h := NewHandler(&fakeRepo{getErr: admindomain.ErrNotFound}, allowAuthorizer{})
	_, err := h.Get(ctxAs(uuid.New(), apiutil.RoleTenantAdmin), uuid.New(), uuid.New())
	if code(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound", code(err))
	}
}

func TestGet_CrossTenantHiddenAsNotFound(t *testing.T) {
	caller := uuid.New()
	// Row belongs to another tenant; a non-platform caller must see NotFound,
	// not PermissionDenied (don't leak existence).
	repo := &fakeRepo{sub: admindomain.EventSubscription{
		SubscriptionID: uuid.New(), TenantID: uuid.New(),
	}}
	h := NewHandler(repo, allowAuthorizer{})
	_, err := h.Get(ctxAs(caller, apiutil.RoleTenantAdmin), repo.sub.TenantID, repo.sub.SubscriptionID)
	if code(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound (cross-tenant hidden)", code(err))
	}
}

func TestGet_Success(t *testing.T) {
	caller := uuid.New()
	id := uuid.New()
	repo := &fakeRepo{sub: admindomain.EventSubscription{SubscriptionID: id, TenantID: caller}}
	h := NewHandler(repo, allowAuthorizer{})
	got, err := h.Get(ctxAs(caller, apiutil.RoleTenantAdmin), caller, id)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.SubscriptionID != id {
		t.Errorf("got %s, want %s", got.SubscriptionID, id)
	}
}

// ─── Update / Delete version mismatch ────────────────────────────────────────

func TestUpdate_VersionMismatchAborts(t *testing.T) {
	caller := uuid.New()
	id := uuid.New()
	repo := &fakeRepo{
		sub:       admindomain.EventSubscription{SubscriptionID: id, TenantID: caller},
		updateErr: admindomain.ErrVersionMismatch,
	}
	h := NewHandler(repo, allowAuthorizer{})
	_, err := h.Update(ctxAs(caller, apiutil.RoleTenantAdmin), caller,
		admindomain.EventSubscription{SubscriptionID: id, TenantID: caller}, 1, nil)
	if code(err) != connect.CodeAborted {
		t.Fatalf("code = %v, want Aborted", code(err))
	}
}

func TestUpdate_InvalidFilterRejectedWhenMasked(t *testing.T) {
	caller := uuid.New()
	id := uuid.New()
	repo := &fakeRepo{sub: admindomain.EventSubscription{SubscriptionID: id, TenantID: caller}}
	h := NewHandler(repo, allowAuthorizer{})
	_, err := h.Update(ctxAs(caller, apiutil.RoleTenantAdmin), caller,
		admindomain.EventSubscription{SubscriptionID: id, TenantID: caller, CELFilter: "bogus_field == 1"},
		0, []string{"filter"})
	if code(err) != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument (filter in mask)", code(err))
	}
}

func TestUpdate_InvalidFilterIgnoredWhenNotMasked(t *testing.T) {
	// A sink-only update carrying a stale/unused filter must not be
	// rejected — validation gates only the field actually being written.
	caller := uuid.New()
	id := uuid.New()
	repo := &fakeRepo{sub: admindomain.EventSubscription{SubscriptionID: id, TenantID: caller}}
	h := NewHandler(repo, allowAuthorizer{})
	_, err := h.Update(ctxAs(caller, apiutil.RoleTenantAdmin), caller,
		admindomain.EventSubscription{SubscriptionID: id, TenantID: caller, CELFilter: "bogus_field == 1"},
		0, []string{"sink"})
	if err != nil {
		t.Fatalf("filter not in mask must not be validated, got: %v", err)
	}
}

func TestDelete_VersionMismatchAborts(t *testing.T) {
	caller := uuid.New()
	id := uuid.New()
	repo := &fakeRepo{
		sub:       admindomain.EventSubscription{SubscriptionID: id, TenantID: caller},
		deleteErr: admindomain.ErrVersionMismatch,
	}
	h := NewHandler(repo, allowAuthorizer{})
	err := h.Delete(ctxAs(caller, apiutil.RoleTenantAdmin), caller, id, 1)
	if code(err) != connect.CodeAborted {
		t.Fatalf("code = %v, want Aborted", code(err))
	}
}

// ─── List ────────────────────────────────────────────────────────────────────

func TestList_NonPlatformForcedToCallerTenant(t *testing.T) {
	caller := uuid.New()
	repo := &fakeRepo{}
	h := NewHandler(repo, allowAuthorizer{})
	// Caller asks for someone else's tenant; the handler must overwrite it.
	_, _, err := h.List(ctxAs(caller, apiutil.RoleTenantAdmin),
		admindomain.ListEventSubscriptionsArgs{TenantID: uuid.New()})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.listArgs.TenantID != caller {
		t.Errorf("list scoped to %s, want the caller's tenant %s", repo.listArgs.TenantID, caller)
	}
}

func TestList_PlatformAdminKeepsRequestedTenant(t *testing.T) {
	want := uuid.New()
	repo := &fakeRepo{}
	h := NewHandler(repo, allowAuthorizer{})
	_, _, err := h.List(ctxAs(uuid.New(), apiutil.RolePlatformAdmin),
		admindomain.ListEventSubscriptionsArgs{TenantID: want})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if repo.listArgs.TenantID != want {
		t.Errorf("platform admin's requested tenant overwritten: got %s want %s",
			repo.listArgs.TenantID, want)
	}
}

// ─── TestSubscription ────────────────────────────────────────────────────────

func newGettableHandler(t *testing.T, caller uuid.UUID, authz cedar.Authorizer) (*Handler, uuid.UUID) {
	t.Helper()
	id := uuid.New()
	repo := &fakeRepo{sub: admindomain.EventSubscription{SubscriptionID: id, TenantID: caller}}
	return NewHandler(repo, authz), id
}

func TestTestSubscription_UnimplementedWithoutDispatcher(t *testing.T) {
	caller := uuid.New()
	h, id := newGettableHandler(t, caller, allowAuthorizer{})
	err := h.TestSubscription(ctxAs(caller, apiutil.RoleTenantAdmin), caller, id)
	if code(err) != connect.CodeUnimplemented {
		t.Fatalf("code = %v, want Unimplemented (no dispatcher)", code(err))
	}
}

func TestTestSubscription_DeliveryFailureMapped(t *testing.T) {
	caller := uuid.New()
	h, id := newGettableHandler(t, caller, allowAuthorizer{})
	h.SetDispatcher(failDispatcher{})
	err := h.TestSubscription(ctxAs(caller, apiutil.RoleTenantAdmin), caller, id)
	if code(err) != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition", code(err))
	}
}

func TestTestSubscription_Success(t *testing.T) {
	caller := uuid.New()
	h, id := newGettableHandler(t, caller, allowAuthorizer{})
	h.SetDispatcher(okDispatcher{})
	if err := h.TestSubscription(ctxAs(caller, apiutil.RoleTenantAdmin), caller, id); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// ─── RedriveFailedDeliveries ─────────────────────────────────────────────────

// actionRecorder allows everything and remembers the action it was asked.
type actionRecorder struct{ action string }

func (a *actionRecorder) IsAuthorized(_ context.Context, _ *cedar.Principal, action string, _ *cedar.Resource, _ cedar.RequestContext) (cedar.Decision, error) {
	a.action = action
	return cedar.DecisionAllow, nil
}

func redriveFixture(authz cedar.Authorizer, disabled bool) (*Handler, *fakeRepo, uuid.UUID, uuid.UUID) {
	caller, id := uuid.New(), uuid.New()
	repo := &fakeRepo{
		sub:      admindomain.EventSubscription{SubscriptionID: id, TenantID: caller, Disabled: disabled},
		requeued: 3,
	}
	return NewHandler(repo, authz), repo, caller, id
}

func TestRedriveFailedDeliveries(t *testing.T) {
	const requeued = 3
	cases := []struct {
		name      string
		authz     cedar.Authorizer
		disabled  bool
		wantCode  connect.Code
		wantCalls int
	}{
		{"queues the failed deliveries", allowAuthorizer{}, false, 0, 1},
		{"refused by policy", denyAuthorizer{}, false, connect.CodePermissionDenied, 0},
		{"refused while the subscription is disabled", allowAuthorizer{}, true, connect.CodeFailedPrecondition, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, repo, caller, id := redriveFixture(tc.authz, tc.disabled)
			n, err := h.RedriveFailedDeliveries(ctxAs(caller, apiutil.RoleTenantAdmin), caller, id)
			if tc.wantCode != 0 {
				if code(err) != tc.wantCode {
					t.Fatalf("code = %v, want %v", code(err), tc.wantCode)
				}
			} else if err != nil {
				t.Fatalf("redrive: %v", err)
			} else if n != requeued {
				t.Errorf("requeued = %d, want %d", n, requeued)
			}
			if repo.requeueCalls != tc.wantCalls {
				t.Errorf("requeue calls = %d, want %d", repo.requeueCalls, tc.wantCalls)
			}
		})
	}
}

// Redriving changes what the subscription delivers, so it takes the
// permission that editing the subscription takes.
func TestRedriveFailedDeliveries_AsksToManageTheSubscription(t *testing.T) {
	rec := &actionRecorder{}
	h, _, caller, id := redriveFixture(rec, false)
	if _, err := h.RedriveFailedDeliveries(ctxAs(caller, apiutil.RoleTenantAdmin), caller, id); err != nil {
		t.Fatalf("redrive: %v", err)
	}
	if rec.action != cedar.ActionManageSubscription {
		t.Errorf("Cedar asked about %q, want %q", rec.action, cedar.ActionManageSubscription)
	}
}

// Compile-time interface assertions.
var (
	_ admindomain.EventSubscriptionRepository = (*fakeRepo)(nil)
	_ Dispatcher                              = okDispatcher{}
	_ cedar.Authorizer                        = allowAuthorizer{}
)
