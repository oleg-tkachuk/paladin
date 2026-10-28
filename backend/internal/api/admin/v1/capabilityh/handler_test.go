package capabilityh

import (
	"context"
	"crypto/ed25519"
	"errors"
	"testing"
	"time"
	"uuid"

	"connectrpc.com/connect/v2"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/protobuf/proto"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/jwks"
	"github.com/oleg-tkachuk/limes/meter"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/captest"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// allowAuthorizer / denyAuthorizer let us assert which Cedar branch the
// handler took: when the capability path is exercised, Cedar must NOT be
// consulted at all (we use denyAuthorizer to surface a regression that
// fell back to the admin path).
type allowAuthorizer struct{ called int }

func (a *allowAuthorizer) IsAuthorized(_ context.Context, _ *cedar.Principal, _ cedar.Action, _ *cedar.Resource, _ cedar.RequestContext) (cedar.Decision, error) {
	a.called++
	return cedar.DecisionAllow, nil
}

type denyAuthorizer struct{ called int }

func (d *denyAuthorizer) IsAuthorized(_ context.Context, _ *cedar.Principal, _ cedar.Action, _ *cedar.Resource, _ cedar.RequestContext) (cedar.Decision, error) {
	d.called++
	return cedar.DecisionDeny, nil
}

// fakeStore is the minimum limes.Store the handler reaches: GetRecord for
// the owner lookup, Insert when the issuer mints, IsRevoked when the
// verifier checks what it minted. Others fail so regressions that touch
// them are loud.
type fakeStore struct {
	cap *limes.Capability
	// inserted counts the capabilities put on record.
	inserted int
}

func (s *fakeStore) Insert(context.Context, limes.InsertRequest) error {
	s.inserted++
	return nil
}
func (s *fakeStore) GetRecord(_ context.Context, id uuid.UUID) (limes.Record, error) {
	if s.cap != nil && s.cap.ID == id {
		return limes.Record{Capability: *s.cap}, nil
	}
	return limes.Record{}, limes.ErrNotFound // the Store contract's sentinel
}
func (s *fakeStore) IsRevoked(context.Context, uuid.UUID) (bool, error) { return false, nil }
func (s *fakeStore) Revoke(context.Context, limes.RevokeRequest) (limes.RevokeResult, error) {
	return limes.RevokeResult{}, errors.New("not used")
}
func (s *fakeStore) Purge(context.Context, limes.PurgeRequest) (limes.PurgeResult, error) {
	return limes.PurgeResult{}, errors.New("not used")
}
func (s *fakeStore) ListByPrincipal(context.Context, limes.ListByPrincipalRequest) (limes.Page[limes.Capability], error) {
	return limes.Page[limes.Capability]{}, errors.New("not used")
}

// newMinter is a captest.Minter whose issuer records into store and whose
// verifier asks store about revocations, so a handler built from it seals
// what it mints over a fake store. It has no store of its own.
func newMinter(t *testing.T, store limes.Store) *captest.Minter {
	t.Helper()
	key, err := limes.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	signer, err := limes.NewSigner(limes.SignerConfig{KeyID: key.KeyID, Key: key.Private})
	if err != nil {
		t.Fatal(err)
	}
	issuer, err := limes.NewIssuer(limes.IssuerConfig{
		Signer: signer, Store: store, IssuerName: captest.Issuer, DefaultTTL: captest.TTL,
	})
	if err != nil {
		t.Fatal(err)
	}
	keys, err := jwks.NewStatic(jwks.StaticConfig{Keys: map[string]ed25519.PublicKey{key.KeyID: key.Public()}})
	if err != nil {
		t.Fatal(err)
	}
	verifier, err := limes.NewVerifier(limes.VerifierConfig{
		Keys: keys, Revocations: store, TrustedIssuers: []string{captest.Issuer},
	})
	if err != nil {
		t.Fatal(err)
	}
	return &captest.Minter{Issuer: issuer, Verifier: verifier}
}

// mkHandler builds a Handler over store, with an issuer and a verifier that
// accept each other's tokens, and store as its record lister when it is one.
func mkHandler(t *testing.T, store limes.Store, usage meter.UsageStore, policy cedar.Authorizer) *Handler {
	t.Helper()
	m := newMinter(t, store)
	h := NewHandler(m.Issuer, m.Verifier, store, usage, policy)
	if records, ok := store.(limes.RecordReporter); ok {
		h.WithRecords(records)
	}
	return h
}

// delegateHandler builds a Handler over m, so the capabilities m mints are
// ones its issuer can delegate from.
func delegateHandler(m *captest.Minter, policy cedar.Authorizer) *Handler {
	return NewHandler(m.Issuer, m.Verifier, m.Store, nil, policy)
}

// delegateTTLSeconds is the lifetime a delegation asks for.
const delegateTTLSeconds = 60

// mintParent mints a capability with ops and returns it verified, as the
// capability interceptor puts it on the context.
func mintParent(t *testing.T, m *captest.Minter, ops ...limes.Op) limes.VerifiedCapability {
	t.Helper()
	v, _ := m.Mint(t, limes.Capability{Caveats: limes.Caveats{Ops: ops}})
	return v
}

func delegateReq(parent limes.VerifiedCapability) *adminv1.CapabilityServiceDelegateRequest {
	return &adminv1.CapabilityServiceDelegateRequest{
		ParentId:   parent.Capability().ID.String(),
		TtlSeconds: delegateTTLSeconds,
	}
}

func mkParent(tenantID uuid.UUID, ops ...limes.Op) limes.Capability {
	now := time.Now().UTC()
	return limes.Capability{
		ID:     uuid.New(),
		Issuer: captest.Issuer,
		Subject: limes.Principal{
			Type:     limes.PrincipalTypeAgent,
			TenantID: tenantID,
			Subject:  "agent-42",
		},
		Audience:  []string{auth.TokenPlaneData},
		IssuedAt:  now,
		ExpiresAt: now.Add(time.Hour),
		Caveats:   limes.Caveats{Ops: ops},
	}
}

// TestDelegate_CapabilityPath_AllowedWithOpShare confirms a capability-
// authenticated caller may delegate when its caveats include OpShare and
// parent_id matches its own ID. Cedar must NOT be consulted — the deny
// authorizer would surface a regression that fell back to the admin
// path. The child comes back as a JWT and as a Biscuit.
func TestDelegate_CapabilityPath_AllowedWithOpShare(t *testing.T) {
	m := captest.NewMinter(t)
	parent := mintParent(t, m, limes.OpGet, limes.OpShare)
	authz := &denyAuthorizer{}
	h := delegateHandler(m, authz)

	resp, err := h.Delegate(auth.WithCapability(context.Background(), parent), delegateReq(parent))
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if resp.GetToken() == "" {
		t.Fatal("expected token, got empty")
	}
	if got := resp.GetCapability().GetParentId(); got != parent.Capability().ID.String() {
		t.Errorf("parent_id = %q, want %s", got, parent.Capability().ID)
	}
	// The Biscuit seals the child, as a verifier accepting Biscuits reads it.
	if b := resp.GetBiscuit(); b == "" {
		t.Error("want the child as a Biscuit beside the token")
	} else if sealed := m.Verify(t, b, captest.Audience); !sealed.FromBiscuit() ||
		sealed.Capability().ID.String() != resp.GetCapability().GetId() {
		t.Errorf("the Biscuit seals %s (from biscuit %t), want the child %s",
			sealed.Capability().ID, sealed.FromBiscuit(), resp.GetCapability().GetId())
	}
	if authz.called != 0 {
		t.Errorf("Cedar consulted %d times; capability path must skip Cedar", authz.called)
	}
}

// insertCtxStore records the context of the last Insert.
type insertCtxStore struct {
	fakeStore
	insertCtx context.Context
}

func (s *insertCtxStore) Insert(ctx context.Context, _ limes.InsertRequest) error {
	s.insertCtx = ctx
	return nil
}

// The child is recorded acting on the parent's tenant, whatever tenant the
// caller's principal is bound to: the store is scoped by it, and under any
// other the parent's record is not there to delegate from.
func TestDelegate_RecordsTheChildInTheParentsTenant(t *testing.T) {
	store := &insertCtxStore{}
	m := newMinter(t, store)
	parent := mintParent(t, m, limes.OpGet, limes.OpShare)
	h := NewHandler(m.Issuer, m.Verifier, store, nil, &denyAuthorizer{})

	ctx := auth.WithCapability(callerCtx(uuid.New(), "platform.admin"), parent)
	if _, err := h.Delegate(ctx, delegateReq(parent)); err != nil {
		t.Fatalf("delegate: %v", err)
	}
	want := parent.Capability().Subject.TenantID
	if got, ok := auth.ActingTenant(store.insertCtx); !ok || got != want {
		t.Errorf("child recorded acting on %v (set %t), want the parent's tenant %v", got, ok, want)
	}
}

// TestDelegate_CapabilityPath_DeniedWithoutOpShare confirms a capability
// without OpShare in its caveats cannot delegate, even if parent_id
// matches.
func TestDelegate_CapabilityPath_DeniedWithoutOpShare(t *testing.T) {
	m := captest.NewMinter(t)
	parent := mintParent(t, m, limes.OpGet, limes.OpList) // no OpShare
	h := delegateHandler(m, &allowAuthorizer{})

	_, err := h.Delegate(auth.WithCapability(context.Background(), parent), delegateReq(parent))
	if got := connect.CodeOf(err); got != connect.CodePermissionDenied {
		t.Errorf("code: got %v, want PermissionDenied (%v)", got, err)
	}
}

// TestDelegate_CapabilityPath_DeniedWithMismatchedParent confirms a
// caller cannot delegate from a parent_id that isn't its own capability
// ID — prevents an agent from minting children off some other principal's
// capability.
func TestDelegate_CapabilityPath_DeniedWithMismatchedParent(t *testing.T) {
	m := captest.NewMinter(t)
	caller := mintParent(t, m, limes.OpShare)
	other := mintParent(t, m, limes.OpShare)
	h := delegateHandler(m, &allowAuthorizer{})

	_, err := h.Delegate(auth.WithCapability(context.Background(), caller), delegateReq(other))
	if got := connect.CodeOf(err); got != connect.CodePermissionDenied {
		t.Errorf("code: got %v, want PermissionDenied (%v)", got, err)
	}
}

// Delegation narrows only a presented capability. There is no admin path
// any more: a caller presenting none — a platform admin included — is told
// to present one, and Cedar is not asked.
func TestDelegate_WithoutAPresentedCapabilityIsFailedPrecondition(t *testing.T) {
	m := captest.NewMinter(t)
	parent := mintParent(t, m, limes.OpGet, limes.OpShare)
	authz := &allowAuthorizer{}
	h := delegateHandler(m, authz)

	ctx := auth.WithPrincipal(context.Background(), &auth.Principal{
		TenantID: parent.Capability().Subject.TenantID,
		Subject:  "ops@example.com",
		Roles:    []string{"platform.admin"},
	})
	_, err := h.Delegate(ctx, delegateReq(parent))
	if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
		t.Fatalf("code: got %v, want FailedPrecondition (%v)", got, err)
	}
	if authz.called != 0 {
		t.Errorf("Cedar consulted %d times; delegation is not Cedar's to grant", authz.called)
	}
}

// The parent's ceilings, which a delegation that sets none keeps.
const (
	parentMaxRequests = 10
	parentBudgetUnits = 5
	childMaxRequests  = 3
)

// A delegation that sets no request ceiling keeps the parent's, and one that
// sets a number narrows it. The budget, never set here, stays the parent's
// throughout.
func TestDelegate_UnsetCeilingKeepsTheParents(t *testing.T) {
	m := captest.NewMinter(t)
	parent, _ := m.Mint(t, limes.Capability{Caveats: limes.Caveats{
		Ops:      []limes.Op{limes.OpGet, limes.OpShare},
		Requests: limes.RequestCeiling{Max: parentMaxRequests},
		Budget:   limes.BudgetCeiling{Max: parentBudgetUnits * limes.NanosPerUnit, UnitCode: platformUnit},
	}})
	h := delegateHandler(m, &denyAuthorizer{})
	parentBudget := &money.Money{CurrencyCode: platformUnit, Units: parentBudgetUnits}

	limit := func(n int64) *int64 { return &n }
	cases := map[string]struct {
		maxRequests  *int64 // nil: unset
		wantRequests int64
	}{
		"unset keeps the parent's": {nil, parentMaxRequests},
		"a number narrows it":      {limit(childMaxRequests), childMaxRequests},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			req := delegateReq(parent)
			req.Caveats = &adminv1.CapabilityCaveats{Ops: []string{string(limes.OpGet)}}
			if tc.maxRequests != nil {
				req.Caveats.RequestCeiling = &adminv1.CapabilityCaveats_MaxRequests{MaxRequests: *tc.maxRequests}
			}
			resp, err := h.Delegate(auth.WithCapability(context.Background(), parent), req)
			if err != nil {
				t.Fatalf("delegate: %v", err)
			}
			got := resp.GetCapability().GetCaveats()
			if _, ok := got.GetRequestCeiling().(*adminv1.CapabilityCaveats_MaxRequests); !ok || got.GetMaxRequests() != tc.wantRequests {
				t.Errorf("request ceiling = %v, want max_requests %d", got.GetRequestCeiling(), tc.wantRequests)
			}
			if !proto.Equal(got.GetMaxBudget(), parentBudget) {
				t.Errorf("max_budget = %v, want the parent's %v", got.GetMaxBudget(), parentBudget)
			}
		})
	}
}

// A child no request could use is refused as the caller's mistake, not
// issued: revoking it is how a capability is stopped.
func TestDelegate_RefusesAZeroRequestCeiling(t *testing.T) {
	m := captest.NewMinter(t)
	parent, _ := m.Mint(t, limes.Capability{Caveats: limes.Caveats{
		Ops:      []limes.Op{limes.OpGet, limes.OpShare},
		Requests: limes.RequestCeiling{Max: parentMaxRequests},
	}})
	h := delegateHandler(m, &denyAuthorizer{})
	req := delegateReq(parent)
	req.Caveats = &adminv1.CapabilityCaveats{
		Ops:            []string{string(limes.OpGet)},
		RequestCeiling: &adminv1.CapabilityCaveats_MaxRequests{MaxRequests: 0},
	}
	if _, err := h.Delegate(auth.WithCapability(context.Background(), parent), req); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("err = %v, want InvalidArgument", err)
	}
}

// A child cannot lift its bounded parent's ceiling by asking for no limit.
func TestDelegate_UnlimitedUnderABoundedParentIsRefused(t *testing.T) {
	m := captest.NewMinter(t)
	parent, _ := m.Mint(t, limes.Capability{Caveats: limes.Caveats{
		Ops:      []limes.Op{limes.OpGet, limes.OpShare},
		Requests: limes.RequestCeiling{Max: parentMaxRequests},
		Budget:   limes.BudgetCeiling{Unlimited: true, UnitCode: platformUnit},
	}})
	h := delegateHandler(m, &denyAuthorizer{})
	req := delegateReq(parent)
	req.Caveats = openCaveats(limes.OpGet)
	if _, err := h.Delegate(auth.WithCapability(context.Background(), parent), req); err == nil {
		t.Fatal("an unlimited child of a bounded parent was delegated")
	}
}
