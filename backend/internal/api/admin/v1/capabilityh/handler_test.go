package capabilityh

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/capability"
	adminv1 "github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1"
	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
)

// allowAuthorizer / denyAuthorizer let us assert which Cedar branch the
// handler took: when the capability path is exercised, Cedar must NOT be
// consulted at all (we use denyAuthorizer to surface a regression that
// fell back to the admin path).
type allowAuthorizer struct{ called int }

func (a *allowAuthorizer) IsAuthorized(_ context.Context, _ *cedar.Principal, _ string, _ *cedar.Resource, _ cedar.RequestContext) (cedar.Decision, error) {
	a.called++
	return cedar.DecisionAllow, nil
}

type denyAuthorizer struct{ called int }

func (d *denyAuthorizer) IsAuthorized(_ context.Context, _ *cedar.Principal, _ string, _ *cedar.Resource, _ cedar.RequestContext) (cedar.Decision, error) {
	d.called++
	return cedar.DecisionDeny, nil
}

// fakeStore is the minimum capability.Store used by Delegate. Get is
// the only method the handler reaches before issuer.Delegate runs;
// Insert is hit by Issuer when the gate allows. Other methods panic so
// regressions that touch them are loud.
type fakeStore struct {
	cap *capability.Capability
}

func (s *fakeStore) Insert(_ context.Context, _ capability.Capability) error { return nil }
func (s *fakeStore) Get(_ context.Context, id uuid.UUID) (*capability.Capability, error) {
	if s.cap != nil && s.cap.ID == id {
		return s.cap, nil
	}
	return nil, errors.New("not found")
}
func (s *fakeStore) IsRevoked(context.Context, uuid.UUID) (bool, error) { return false, nil }
func (s *fakeStore) Revoke(context.Context, capability.RevokeArgs) error {
	return errors.New("not used")
}
func (s *fakeStore) PurgeExpired(context.Context, time.Duration) (int64, error) {
	return 0, errors.New("not used")
}
func (s *fakeStore) ListByPrincipal(context.Context, capability.ListByPrincipalArgs) ([]capability.Capability, string, error) {
	return nil, "", errors.New("not used")
}

func mkIssuer(t *testing.T, store capability.Store) *capability.Issuer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("genkey: %v", err)
	}
	signer, err := capability.NewEd25519Signer("k1", priv)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	iss, err := capability.NewIssuer(capability.IssuerConfig{
		Signer:     signer,
		Store:      store,
		IssuerName: "paladin-test",
		DefaultTTL: time.Minute,
	})
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}
	return iss
}

func mkParent(tenantID uuid.UUID, ops ...capability.Op) capability.Capability {
	now := time.Now().UTC()
	return capability.Capability{
		ID:     uuid.New(),
		Issuer: "paladin-test",
		Subject: capability.Principal{
			Type:     capability.PrincipalAgent,
			TenantID: tenantID,
			Subject:  "agent-42",
		},
		Audience:   []string{capability.AudiencePlaneData},
		IssuedAt:   now,
		ExpiresAt:  now.Add(time.Hour),
		Generation: 1,
		Caveats:    capability.Caveats{Ops: ops},
	}
}

// TestDelegate_CapabilityPath_AllowedWithOpShare confirms a capability-
// authenticated caller may delegate when its caveats include OpShare and
// parent_id matches its own ID. Cedar must NOT be consulted — the deny
// authorizer would surface a regression that fell back to the admin
// path.
func TestDelegate_CapabilityPath_AllowedWithOpShare(t *testing.T) {
	tenantID := uuid.New()
	parent := mkParent(tenantID, capability.OpGet, capability.OpShare)
	store := &fakeStore{cap: &parent}
	issuer := mkIssuer(t, store)
	authz := &denyAuthorizer{}
	h := NewHandler(issuer, store, nil, authz)

	ctx := auth.WithCapability(context.Background(), &parent)
	resp, err := h.Delegate(ctx, connect.NewRequest(&adminv1.CapabilityServiceDelegateRequest{
		ParentId:   parent.ID.String(),
		TtlSeconds: 60,
	}))
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if resp.Msg.GetToken() == "" {
		t.Fatal("expected token, got empty")
	}
	if authz.called != 0 {
		t.Errorf("Cedar consulted %d times; capability path must skip Cedar", authz.called)
	}
}

// TestDelegate_CapabilityPath_DeniedWithoutOpShare confirms a capability
// without OpShare in its caveats cannot delegate, even if parent_id
// matches.
func TestDelegate_CapabilityPath_DeniedWithoutOpShare(t *testing.T) {
	tenantID := uuid.New()
	parent := mkParent(tenantID, capability.OpGet, capability.OpList) // no OpShare
	store := &fakeStore{cap: &parent}
	issuer := mkIssuer(t, store)
	h := NewHandler(issuer, store, nil, &allowAuthorizer{})

	ctx := auth.WithCapability(context.Background(), &parent)
	_, err := h.Delegate(ctx, connect.NewRequest(&adminv1.CapabilityServiceDelegateRequest{
		ParentId:   parent.ID.String(),
		TtlSeconds: 60,
	}))
	if err == nil {
		t.Fatal("expected PermissionDenied, got nil")
	}
	if got := connect.CodeOf(err); got != connect.CodePermissionDenied {
		t.Errorf("code: got %v, want PermissionDenied", got)
	}
}

// TestDelegate_CapabilityPath_DeniedWithMismatchedParent confirms a
// caller cannot delegate from a parent_id that isn't its own capability
// ID — prevents an agent from minting children off some other principal's
// capability.
func TestDelegate_CapabilityPath_DeniedWithMismatchedParent(t *testing.T) {
	tenantID := uuid.New()
	caller := mkParent(tenantID, capability.OpShare)
	other := mkParent(tenantID, capability.OpShare)
	store := &fakeStore{cap: &other}
	issuer := mkIssuer(t, store)
	h := NewHandler(issuer, store, nil, &allowAuthorizer{})

	ctx := auth.WithCapability(context.Background(), &caller)
	_, err := h.Delegate(ctx, connect.NewRequest(&adminv1.CapabilityServiceDelegateRequest{
		ParentId:   other.ID.String(), // not caller's ID
		TtlSeconds: 60,
	}))
	if err == nil {
		t.Fatal("expected PermissionDenied, got nil")
	}
	if got := connect.CodeOf(err); got != connect.CodePermissionDenied {
		t.Errorf("code: got %v, want PermissionDenied", got)
	}
}

// TestDelegate_AdminPath_StillRequiresCedar confirms the admin path is
// preserved: when no capability is on the context, the handler runs the
// existing Cedar check.
func TestDelegate_AdminPath_StillRequiresCedar(t *testing.T) {
	tenantID := uuid.New()
	parent := mkParent(tenantID, capability.OpGet, capability.OpShare)
	store := &fakeStore{cap: &parent}
	issuer := mkIssuer(t, store)
	authz := &allowAuthorizer{}
	h := NewHandler(issuer, store, nil, authz)

	// Plain admin principal, no capability on context.
	ctx := auth.WithPrincipal(context.Background(), &auth.Principal{
		TenantID: tenantID,
		Subject:  "ops@example.com",
		Roles:    []string{"platform.admin"},
	})

	if _, err := h.Delegate(ctx, connect.NewRequest(&adminv1.CapabilityServiceDelegateRequest{
		ParentId:   parent.ID.String(),
		TtlSeconds: 60,
	})); err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if authz.called == 0 {
		t.Error("admin path must consult Cedar")
	}
}
