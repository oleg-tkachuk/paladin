package backendh

import (
	"context"
	"testing"

	"connectrpc.com/connect"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
)

// allowAuthorizer is a test stub that uniformly approves every action. The
// role-guard tests below rely on requirePlatformAdmin firing before Cedar,
// so a no-op authorizer keeps the role assertions surgical.
type allowAuthorizer struct{}

func (allowAuthorizer) IsAuthorized(_ context.Context, _ *cedar.Principal, _ string, _ *cedar.Resource, _ cedar.RequestContext) (cedar.Decision, error) {
	return cedar.DecisionAllow, nil
}

type fakeBackendRepo struct{}

func (fakeBackendRepo) Upsert(context.Context, admindomain.StorageBackend) error { return nil }
func (fakeBackendRepo) Get(context.Context, string) (admindomain.StorageBackend, error) {
	return admindomain.StorageBackend{BackendID: "primary", Kind: "s3-compatible"}, nil
}
func (fakeBackendRepo) List(context.Context, int32, string) ([]admindomain.StorageBackend, string, error) {
	return nil, "", nil
}
func (fakeBackendRepo) Update(context.Context, admindomain.StorageBackend, int64, []string) error {
	return nil
}
func (fakeBackendRepo) SetEnabled(context.Context, string, bool, int64) error   { return nil }
func (fakeBackendRepo) RotateCredentials(context.Context, string, string) error { return nil }
func (fakeBackendRepo) Delete(context.Context, string, int64, bool) error       { return nil }

func ctxWithRoles(roles ...string) context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{
		Subject: "tester",
		Roles:   roles,
	})
}

func TestCreateBackendRequiresPlatformAdmin(t *testing.T) {
	h := NewHandler(fakeBackendRepo{}, allowAuthorizer{}, "")
	_, err := h.CreateBackend(ctxWithRoles("tenant.admin"), admindomain.StorageBackend{
		BackendID: "primary", Kind: "s3-compatible",
	})
	if err == nil {
		t.Fatal("expected denial for non-platform.admin caller")
	}
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("got %v want PermissionDenied", connect.CodeOf(err))
	}
}

func TestCreateBackendAllowsPlatformAdmin(t *testing.T) {
	h := NewHandler(fakeBackendRepo{}, allowAuthorizer{}, "") // nil engine → role-only path
	out, err := h.CreateBackend(ctxWithRoles("platform.admin"), admindomain.StorageBackend{
		BackendID: "primary", Kind: "s3-compatible",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out.BackendID != "primary" {
		t.Errorf("got %q", out.BackendID)
	}
}

func TestGetBackendRedactsSecretForNonPlatformAdmin(t *testing.T) {
	repo := redactingRepo{}
	h := NewHandler(repo, allowAuthorizer{}, "")
	out, err := h.GetBackend(ctxWithRoles("tenant.admin"), "primary")
	if err != nil {
		t.Fatal(err)
	}
	if out.CredentialsSecretRef != "[REDACTED]" {
		t.Errorf("expected redaction for tenant.admin, got %q", out.CredentialsSecretRef)
	}
}

func TestGetBackendUnredactedForPlatformAdmin(t *testing.T) {
	repo := redactingRepo{}
	h := NewHandler(repo, allowAuthorizer{}, "")
	out, err := h.GetBackend(ctxWithRoles("platform.admin"), "primary")
	if err != nil {
		t.Fatal(err)
	}
	if out.CredentialsSecretRef != "vault://kv/paladin/primary" {
		t.Errorf("platform.admin should see real secret_ref, got %q", out.CredentialsSecretRef)
	}
}

// redactingRepo returns a backend with a real secret_ref so the handler's
// per-role redaction can be exercised.
type redactingRepo struct{ fakeBackendRepo }

func (redactingRepo) Get(context.Context, string) (admindomain.StorageBackend, error) {
	return admindomain.StorageBackend{
		BackendID:            "primary",
		Kind:                 "s3-compatible",
		CredentialsSecretRef: "vault://kv/paladin/primary",
	}, nil
}

// Note on Cedar gating: end-to-end authz behaviour is covered by the Cedar
// unit tests. Here we inject a permissive Authorizer so the assertions stay
// focused on the role-guard layer (requirePlatformAdmin) — Cedar passes
// uniformly, so any rejection surfaces an RBAC bug rather than a policy
// rule.
