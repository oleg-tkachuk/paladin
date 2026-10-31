package capabilityh

import (
	"context"
	"errors"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// fakeRuns records the run revocation asked of it.
type fakeRuns struct {
	tenant, run uuid.UUID
	note, actor string
	acting      uuid.UUID
	calls       int
	revoked     int64
	err         error
}

func (f *fakeRuns) RevokeRun(ctx context.Context, tenant, run uuid.UUID, note, actor string) (int64, error) {
	f.calls++
	f.tenant, f.run, f.note, f.actor = tenant, run, note, actor
	f.acting, _ = auth.ActingTenant(ctx)
	return f.revoked, f.err
}

func runRequest(tenant, run uuid.UUID) *adminv1.CapabilityServiceRevokeRunRequest {
	return &adminv1.CapabilityServiceRevokeRunRequest{TenantId: tenant.String(), RunId: run.String(), Reason: "runaway agent"}
}

// The run is revoked in the tenant named, acting on it, as the caller, with
// the reason given; the answer counts what it revoked.
func TestRevokeRun(t *testing.T) {
	tenant, run := uuid.New(), uuid.New()
	runs := &fakeRuns{revoked: 3}
	h := mkHandler(t, &fakeStore{}, nil, &allowAuthorizer{}).WithRunRevocation(runs)
	ctx := apiutil.WithResourceSlot(tenantCtx(tenant))
	res, err := h.RevokeRun(ctx, runRequest(tenant, run))
	if err != nil {
		t.Fatal(err)
	}
	// The audit row names the run under its tenant.
	if got, want := apiutil.ResourceFromContext(ctx), "tenants/"+tenant.String()+"/runs/"+run.String(); got != want {
		t.Errorf("audited %q, want %q", got, want)
	}
	if res.GetRevoked() != 3 {
		t.Errorf("revoked = %d, want 3", res.GetRevoked())
	}
	if runs.tenant != tenant || runs.run != run || runs.acting != tenant ||
		runs.note != "runaway agent" || runs.actor != "tenant-operator" {
		t.Errorf("asked %+v, want run %s of tenant %s, acting on it, by tenant-operator", runs, run, tenant)
	}
}

func TestRevokeRunAnotherTenant(t *testing.T) {
	other, run := uuid.New(), uuid.New()

	// A caller confined to its tenant is refused another's run.
	runs := &fakeRuns{}
	h := mkHandler(t, &fakeStore{}, nil, &allowAuthorizer{}).WithRunRevocation(runs)
	if _, err := h.RevokeRun(tenantCtx(uuid.New()), runRequest(other, run)); codeOf(err) != connect.CodePermissionDenied || runs.calls != 0 {
		t.Errorf("code = %v after %d calls, want PermissionDenied before any", codeOf(err), runs.calls)
	}
	// A platform admin may revoke any tenant's.
	if _, err := h.RevokeRun(adminCtx(), runRequest(other, run)); err != nil || runs.tenant != other {
		t.Errorf("platform admin: %v, revoked in %s; want %s", err, runs.tenant, other)
	}
}

func TestRevokeRunRefusals(t *testing.T) {
	tenant, run := uuid.New(), uuid.New()
	cases := map[string]struct {
		h    *Handler
		req  *adminv1.CapabilityServiceRevokeRunRequest
		want connect.Code
	}{
		"denied by policy":  {mkHandler(t, &fakeStore{}, nil, &denyAuthorizer{}).WithRunRevocation(&fakeRuns{}), runRequest(tenant, run), connect.CodePermissionDenied},
		"not wired":         {mkHandler(t, &fakeStore{}, nil, &allowAuthorizer{}), runRequest(tenant, run), connect.CodeUnavailable},
		"a bad run":         {mkHandler(t, &fakeStore{}, nil, &allowAuthorizer{}).WithRunRevocation(&fakeRuns{}), &adminv1.CapabilityServiceRevokeRunRequest{TenantId: tenant.String(), RunId: "nope"}, connect.CodeInvalidArgument},
		"a bad tenant":      {mkHandler(t, &fakeStore{}, nil, &allowAuthorizer{}).WithRunRevocation(&fakeRuns{}), &adminv1.CapabilityServiceRevokeRunRequest{TenantId: "nope", RunId: run.String()}, connect.CodeInvalidArgument},
		"the store failing": {mkHandler(t, &fakeStore{}, nil, &allowAuthorizer{}).WithRunRevocation(&fakeRuns{err: errors.New("db down")}), runRequest(tenant, run), connect.CodeUnavailable},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := tc.h.RevokeRun(tenantCtx(tenant), tc.req); codeOf(err) != tc.want {
				t.Errorf("code = %v, want %v (%v)", codeOf(err), tc.want, err)
			}
		})
	}
}

// A listing narrows to one run: what RevokeRun would revoke.
func TestListNarrowsToARun(t *testing.T) {
	store := &recordingStore{}
	h := mkHandler(t, store, nil, &allowAuthorizer{})
	run := uuid.New()
	if _, err := h.List(adminCtx(), &adminv1.CapabilityServiceListRequest{TenantId: uuid.New().String(), RunId: run.String()}); err != nil {
		t.Fatal(err)
	}
	if store.listArgs.RunID != run {
		t.Errorf("listed run %s, want %s", store.listArgs.RunID, run)
	}
	if _, err := h.List(adminCtx(), &adminv1.CapabilityServiceListRequest{TenantId: uuid.New().String(), RunId: "nope"}); codeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("a bad run: code = %v, want InvalidArgument", codeOf(err))
	}
	if _, err := h.List(adminCtx(), &adminv1.CapabilityServiceListRequest{TenantId: uuid.New().String()}); err != nil || store.listArgs.RunID != uuid.Nil() {
		t.Errorf("no run: %v, listed run %s; want every run's", err, store.listArgs.RunID)
	}
}
