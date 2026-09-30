package tenant

import (
	"context"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/v1/apiutil"
)

// provisionerCtx carries ONLY platform.tenant-provisioner — no platform.admin.
// Every test here asks what that credential alone can do.
func provisionerCtx(tid uuid.UUID) context.Context {
	return principalCtx(tid, apiutil.RoleTenantProvisioner)
}

// Creating a tenant is the whole reason the role exists: a consumer signs up,
// and its tenant has to come into being without a human running a command.
func TestCreateTenant_AllowsTenantProvisioner(t *testing.T) {
	h := NewHandler(&fakeRepo{}, allow())

	_, err := h.CreateTenant(provisionerCtx(uuid.New()), CreateTenantArgs{
		TenantID: uuid.New(),
		Slug:     "acme-abc123",
	})
	if err != nil {
		t.Fatalf("CreateTenant as tenant-provisioner: %v", err)
	}
}

// It reads the tenant it is provisioning — to learn whether it exists, and to
// carry resource_version into the policy write — for a tenant that is not its
// own. That cross-tenant read used to require platform.admin.
func TestGetTenant_AllowsTenantProvisionerCrossTenant(t *testing.T) {
	other := uuid.New()
	h := NewHandler(&fakeRepo{getFn: func(context.Context, uuid.UUID) (Tenant, error) {
		return Tenant{TenantID: other, Slug: "acme-abc123"}, nil
	}}, allow())

	if _, err := h.GetTenant(provisionerCtx(uuid.New()), other); err != nil {
		t.Fatalf("GetTenant as tenant-provisioner: %v", err)
	}
}

// SetInheritedPolicy routes through UpdateTenant, and that is the ONE edit the
// role may make: a fresh tenant is unusable until its policy admits the
// consumer.
func TestUpdateTenant_AllowsTenantProvisionerForPolicyOnly(t *testing.T) {
	h := NewHandler(&fakeRepo{}, allow())

	_, err := h.UpdateTenant(provisionerCtx(uuid.New()), UpdateTenantArgs{
		TenantID:             uuid.New(),
		InheritedCedarPolicy: strptr(`permit (principal, action, resource);`),
	})
	if err != nil {
		t.Fatalf("policy-only UpdateTenant as tenant-provisioner: %v", err)
	}
}

// The gate is on the SHAPE of the update, not on the RPC that produced it.
// A provisioner smuggling a display-name change alongside the policy is refused
// outright rather than having the extra field quietly dropped — a silent drop
// would make the credential's real authority differ from its documented one.
func TestUpdateTenant_RefusesTenantProvisionerBeyondPolicy(t *testing.T) {
	h := NewHandler(&fakeRepo{}, allow())
	ctx := provisionerCtx(uuid.New())

	t.Run("display name alongside policy", func(t *testing.T) {
		_, err := h.UpdateTenant(ctx, UpdateTenantArgs{
			TenantID:             uuid.New(),
			InheritedCedarPolicy: strptr(`permit (principal, action, resource);`),
			DisplayName:          strptr("renamed by a provisioner"),
		})
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("labels alongside policy", func(t *testing.T) {
		_, err := h.UpdateTenant(ctx, UpdateTenantArgs{
			TenantID:             uuid.New(),
			InheritedCedarPolicy: strptr(`permit (principal, action, resource);`),
			Labels:               []byte(`{"tier":"gold"}`),
		})
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("display name alone", func(t *testing.T) {
		_, err := h.UpdateTenant(ctx, UpdateTenantArgs{
			TenantID:    uuid.New(),
			DisplayName: strptr("renamed by a provisioner"),
		})
		wantCode(t, err, connect.CodePermissionDenied)
	})
}

// The role adds tenants and never removes one. These are the RPCs that destroy
// or rewrite a tenant's identity, and they stay platform.admin-only — which is
// what makes a leaked provisioner credential survivable.
func TestTenantLifecycle_StaysPlatformAdminOnly(t *testing.T) {
	tid := uuid.New()
	ctx := provisionerCtx(uuid.New())

	t.Run("DeleteTenant", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, allow())
		wantCode(t, h.DeleteTenant(ctx, tid, 1), connect.CodePermissionDenied)
	})

	t.Run("PurgeTenant", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, allow())
		wantCode(t, h.PurgeTenant(ctx, tid), connect.CodePermissionDenied)
	})

	t.Run("RestoreTenant", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, allow())
		_, err := h.RestoreTenant(ctx, tid)
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("RenameTenantSlug", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, allow())
		_, err := h.RenameTenantSlug(ctx, RenameTenantSlugArgs{TenantID: tid, NewSlug: "taken-over"})
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("MigrateTenantStorageLayout", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, allow())
		_, err := h.MigrateTenantStorageLayout(ctx, tid, "other-backend", 0)
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("ListTenants", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, allow())
		_, _, err := h.ListTenants(ctx, ListTenantsArgs{}, "")
		wantCode(t, err, connect.CodePermissionDenied)
	})
}

// A principal with neither role is still refused everywhere, including the
// widened gates. Guards against a helper that admits anyone authenticated.
func TestProvisioningGates_RefuseAnUnprivilegedPrincipal(t *testing.T) {
	ctx := principalCtx(uuid.New(), "tenant.user")

	t.Run("CreateTenant", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, allow())
		_, err := h.CreateTenant(ctx, CreateTenantArgs{TenantID: uuid.New(), Slug: "acme-abc123"})
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("GetTenant cross-tenant", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, allow())
		_, err := h.GetTenant(ctx, uuid.New())
		wantCode(t, err, connect.CodePermissionDenied)
	})

	t.Run("policy-only UpdateTenant", func(t *testing.T) {
		h := NewHandler(&fakeRepo{}, allow())
		_, err := h.UpdateTenant(ctx, UpdateTenantArgs{
			TenantID:             uuid.New(),
			InheritedCedarPolicy: strptr(`permit (principal, action, resource);`),
		})
		wantCode(t, err, connect.CodePermissionDenied)
	})
}
