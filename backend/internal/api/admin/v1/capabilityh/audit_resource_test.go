package capabilityh

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/capability"
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
	h := NewHandler(mkIssuer(t, store), store, nil, &allowAuthorizer{})
	tenant := uuid.New()

	ctx := withSlot(callerCtx(uuid.New(), "platform.admin"))
	resp, err := h.Issue(ctx, connect.NewRequest(&adminv1.CapabilityServiceIssueRequest{
		Subject: &adminv1.CapabilityPrincipal{
			Kind: adminv1.PrincipalKind_PRINCIPAL_KIND_USER, TenantId: tenant.String(), Subject: "alice",
		},
		Audience:   []string{"paladin-data"},
		TtlSeconds: 300,
		Caveats:    &adminv1.CapabilityCaveats{Ops: []string{string(capability.OpGet)}},
	}))
	if err != nil {
		t.Fatalf("Issue: %v", err)
	}
	id := uuid.MustParse(resp.Msg.GetCapability().GetId())
	if got, want := apiutil.ResourceFromContext(ctx), capabilityResourceName(tenant, id); got != want {
		t.Errorf("resource = %q, want %q", got, want)
	}
}

func TestDelegateNamesTheChildUnderItsTenant(t *testing.T) {
	tenant := uuid.New()
	parent := mkParent(tenant, capability.OpGet, capability.OpShare)
	store := &fakeStore{cap: &parent}
	h := NewHandler(mkIssuer(t, store), store, nil, &denyAuthorizer{})

	ctx := withSlot(auth.WithCapability(context.Background(), &parent))
	resp, err := h.Delegate(ctx, connect.NewRequest(&adminv1.CapabilityServiceDelegateRequest{
		ParentId:   parent.ID.String(),
		TtlSeconds: 60,
	}))
	if err != nil {
		t.Fatalf("Delegate: %v", err)
	}
	child := uuid.MustParse(resp.Msg.GetCapability().GetId())
	if got, want := apiutil.ResourceFromContext(ctx), capabilityResourceName(tenant, child); got != want {
		t.Errorf("resource = %q, want %q", got, want)
	}
}

// Revoke names the capability's tenant — the owner's for an admin, who acts
// across tenants, and the caller's own for a caller confined to it.
func TestRevokeNamesTheCapabilityUnderItsTenant(t *testing.T) {
	owner := uuid.New()
	target := mkParent(owner, capability.OpGet)
	cases := map[string]context.Context{
		"platform admin":         callerCtx(uuid.New(), "platform.admin"),
		"tenant-confined caller": callerCtx(owner),
	}
	for name, caller := range cases {
		t.Run(name, func(t *testing.T) {
			store := &lookupStore{recordingStore: recordingStore{fakeStore: fakeStore{cap: &target}}}
			h := NewHandler(mkIssuer(t, &store.fakeStore), store, nil, &allowAuthorizer{})
			ctx := withSlot(caller)
			if _, err := h.Revoke(ctx, connect.NewRequest(&adminv1.CapabilityServiceRevokeRequest{
				Id: target.ID.String(),
			})); err != nil {
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
	store := &recordingStore{revokeErr: capability.ErrNotFound}
	h := NewHandler(mkIssuer(t, &store.fakeStore), store, nil, &allowAuthorizer{})
	ctx := withSlot(callerCtx(uuid.New()))
	_, err := h.Revoke(ctx, connect.NewRequest(&adminv1.CapabilityServiceRevokeRequest{Id: uuid.NewString()}))
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("err = %v, want NotFound", err)
	}
	if got := apiutil.ResourceFromContext(ctx); got != "" {
		t.Errorf("resource = %q after a refused revoke, want none", got)
	}
}

func TestRevokeBiscuitNamesTheCapabilityUnderItsTenant(t *testing.T) {
	owner := uuid.New()
	target := mkParent(owner, capability.OpGet)
	store := &lookupStore{recordingStore: recordingStore{fakeStore: fakeStore{cap: &target}}}
	copier := &fakeCopier{copy: capability.BiscuitCopy{CapabilityID: target.ID, RevocationID: []byte("last-block")}}
	h := NewHandler(mkIssuer(t, &store.fakeStore), store, nil, &allowAuthorizer{}).WithBiscuitCopies(copier, &copyStore{})

	ctx := withSlot(callerCtx(uuid.New(), "platform.admin"))
	if _, err := h.RevokeBiscuit(ctx, revokeBiscuitReq()); err != nil {
		t.Fatalf("RevokeBiscuit: %v", err)
	}
	if got, want := apiutil.ResourceFromContext(ctx), capabilityResourceName(owner, target.ID); got != want {
		t.Errorf("resource = %q, want %q", got, want)
	}

	refused := withSlot(callerCtx(uuid.New(), "platform.admin"))
	h = NewHandler(mkIssuer(t, &store.fakeStore), store, nil, &allowAuthorizer{}).
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
