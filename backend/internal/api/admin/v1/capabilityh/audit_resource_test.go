package capabilityh

import (
	"context"
	"errors"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/captest"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// The capability RPCs address a capability by id, or not at all, so the audit
// row named no resource and the call was in no tenant's trail: a platform
// admin issuing or revoking inside tenant T left a row only T's page could
// not show. Each now names the capability under its tenant.

// withSlot is a request context as the audit interceptor hands it over.
func withSlot(ctx context.Context) context.Context { return apiutil.WithResourceSlot(ctx) }

func TestIssueNamesTheCapabilityUnderItsTenant(t *testing.T) {
	store := &fakeStore{}
	h := mkHandler(t, store, nil, &allowAuthorizer{})
	tenant := uuid.New()

	ctx := withSlot(callerCtx(uuid.New(), "platform.admin"))
	resp, err := h.Issue(ctx, &adminv1.CapabilityServiceIssueRequest{
		Subject: &adminv1.CapabilityPrincipal{
			Kind: adminv1.PrincipalKind_PRINCIPAL_KIND_USER, TenantId: tenant.String(), Subject: "alice",
		},
		Audience:   []string{"paladin-data"},
		TtlSeconds: 300,
		Caveats:    openCaveats(limes.OpGet),
	})
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	id := uuid.MustParse(resp.GetCapability().GetId())
	if got, want := apiutil.ResourceFromContext(ctx), capabilityResourceName(tenant, id); got != want {
		t.Errorf("resource = %q, want %q", got, want)
	}
}

func TestDelegateNamesTheChildUnderItsTenant(t *testing.T) {
	m := captest.NewMinter(t)
	parent := mintParent(t, m, limes.OpGet, limes.OpShare)
	tenant := parent.Capability().Subject.TenantID
	h := delegateHandler(m, &denyAuthorizer{})

	ctx := withSlot(auth.WithCapability(context.Background(), parent))
	resp, err := h.Delegate(ctx, delegateReq(parent))
	if err != nil {
		t.Fatalf("Delegate: %v", err)
	}
	child := uuid.MustParse(resp.GetCapability().GetId())
	if got, want := apiutil.ResourceFromContext(ctx), capabilityResourceName(tenant, child); got != want {
		t.Errorf("resource = %q, want %q", got, want)
	}
}

// Revoke names the capability's tenant — the owner's for an admin, who acts
// across tenants, and the caller's own for a caller confined to it.
func TestRevokeNamesTheCapabilityUnderItsTenant(t *testing.T) {
	owner := uuid.New()
	target := mkParent(owner, limes.OpGet)
	cases := map[string]context.Context{
		"platform admin":         callerCtx(uuid.New(), "platform.admin"),
		"tenant-confined caller": callerCtx(owner),
	}
	for name, caller := range cases {
		t.Run(name, func(t *testing.T) {
			store := &lookupStore{recordingStore: recordingStore{fakeStore: fakeStore{cap: &target}}}
			h := mkHandler(t, store, nil, &allowAuthorizer{})
			ctx := withSlot(caller)
			if _, err := h.Revoke(ctx, &adminv1.CapabilityServiceRevokeRequest{
				Id: target.ID.String(),
			}); err != nil {
				t.Fatalf("Revoke: %v", err)
			}
			if got, want := apiutil.ResourceFromContext(ctx), capabilityResourceName(owner, target.ID); got != want {
				t.Errorf("resource = %q, want %q", got, want)
			}
		})
	}
}

// A revoke the store refused names nothing: for a caller confined to its
// tenant, NotFound may mean another tenant's capability, whose tenant the
// handler does not know.
func TestRevokeRefusedNamesNothing(t *testing.T) {
	store := &recordingStore{revokeErr: limes.ErrNotFound}
	h := mkHandler(t, store, nil, &allowAuthorizer{})
	ctx := withSlot(callerCtx(uuid.New()))
	_, err := h.Revoke(ctx, &adminv1.CapabilityServiceRevokeRequest{Id: uuid.New().String()})
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("err = %v, want NotFound", err)
	}
	if got := apiutil.ResourceFromContext(ctx); got != "" {
		t.Errorf("resource = %q after a refused revoke, want none", got)
	}
}

func TestRevokeBiscuitNamesTheCapabilityUnderItsTenant(t *testing.T) {
	owner := uuid.New()
	target, copied := mintCopy(t, owner, limes.Capability{})
	store := &lookupStore{recordingStore: recordingStore{fakeStore: fakeStore{cap: &target}}}
	copier := &fakeCopier{copy: copied}
	h := mkHandler(t, store, nil, &allowAuthorizer{}).WithBiscuitCopies(copier, &copyStore{})

	ctx := withSlot(callerCtx(uuid.New(), "platform.admin"))
	if _, err := h.RevokeBiscuit(ctx, revokeBiscuitReq()); err != nil {
		t.Fatalf("RevokeBiscuit: %v", err)
	}
	if got, want := apiutil.ResourceFromContext(ctx), capabilityResourceName(owner, target.ID); got != want {
		t.Errorf("resource = %q, want %q", got, want)
	}

	refused := withSlot(callerCtx(uuid.New(), "platform.admin"))
	h = mkHandler(t, store, nil, &allowAuthorizer{}).
		WithBiscuitCopies(copier, &copyStore{err: errors.New("down")})
	if _, err := h.RevokeBiscuit(refused, revokeBiscuitReq()); err == nil {
		t.Fatal("RevokeBiscuit succeeded against a failing store")
	}
	if got := apiutil.ResourceFromContext(refused); got != "" {
		t.Errorf("resource = %q after a failed revoke, want none", got)
	}
}

// The name parses back to the tenant, which is what files the row.
func TestCapabilityResourceNameCarriesTheTenant(t *testing.T) {
	tenant, id := uuid.New(), uuid.New()
	got, ok := apiutil.TenantInResourceName(capabilityResourceName(tenant, id))
	if !ok || got != tenant {
		t.Errorf("tenant in %q = %s (%v), want %s", capabilityResourceName(tenant, id), got, ok, tenant)
	}
}
