package capabilityh

import (
	"bytes"
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/captest"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// fakeCopier names a fixed copy, or fails, and records the token it read.
type fakeCopier struct {
	copy  limes.BiscuitCopy
	err   error
	token string
}

func (c *fakeCopier) BiscuitCopy(_ context.Context, token string) (limes.BiscuitCopy, error) {
	c.token = token
	return c.copy, c.err
}

// copyStore records what RevokeBiscuit was asked to list, and on which context.
type copyStore struct {
	args *limes.RevokeBiscuitRequest
	ctx  context.Context
	res  limes.RevokeBiscuitResult
	err  error
}

func (s *copyStore) IsBiscuitRevoked(context.Context, [][]byte) (bool, error) { return false, nil }
func (s *copyStore) GetBiscuitRevocation(context.Context, limes.BiscuitCopy) (limes.BiscuitRevocation, error) {
	return limes.BiscuitRevocation{}, limes.ErrNotFound
}
func (s *copyStore) RevokeBiscuit(ctx context.Context, args limes.RevokeBiscuitRequest) (limes.RevokeBiscuitResult, error) {
	s.args, s.ctx = &args, ctx
	return s.res, s.err
}

// mintCopy mints a capability for owner shaped as template, seals it as a
// Biscuit, narrows that by each attenuation in turn and returns the
// capability with the copy the verifier names: only a verifier makes a
// limes.BiscuitCopy.
func mintCopy(t *testing.T, owner uuid.UUID, template limes.Capability, as ...limes.Attenuation) (limes.Capability, limes.BiscuitCopy) {
	t.Helper()
	ctx := context.Background()
	m := captest.NewMinter(t)
	template.Subject.TenantID = owner
	// limes seals a Biscuit only for a capability that may be shared.
	if len(template.Caveats.Ops) == 0 {
		template.Caveats.Ops = []limes.Op{limes.OpGet}
	}
	if !slices.Contains(template.Caveats.Ops, limes.OpShare) {
		template.Caveats.Ops = append(template.Caveats.Ops, limes.OpShare)
	}
	v, _ := m.Mint(t, template)
	b, err := m.Issuer.Biscuit(ctx, limes.BiscuitRequest{Capability: v})
	if err != nil {
		t.Fatalf("biscuit: %v", err)
	}
	token := b.Token
	for _, a := range as {
		if token, err = limes.Attenuate(token, a); err != nil {
			t.Fatalf("attenuate: %v", err)
		}
	}
	c, err := m.Verifier.BiscuitCopy(ctx, token)
	if err != nil {
		t.Fatalf("biscuit copy: %v", err)
	}
	return v.Capability(), c
}

func revokeBiscuitReq() *adminv1.CapabilityServiceRevokeBiscuitRequest {
	return &adminv1.CapabilityServiceRevokeBiscuitRequest{Token: "biscuit", Reason: "leaked"}
}

func TestRevokeBiscuitListsTheCopy(t *testing.T) {
	owner := uuid.New()
	target, copied := mintCopy(t, owner, limes.Capability{})
	store := &lookupStore{recordingStore: recordingStore{fakeStore: fakeStore{cap: &target}}}
	copier := &fakeCopier{copy: copied}
	copies := &copyStore{}
	h := mkHandler(t, store, nil, &allowAuthorizer{}).WithBiscuitCopies(copier, copies)

	resp, err := h.RevokeBiscuit(callerCtx(uuid.New(), "platform.admin"), revokeBiscuitReq())
	if err != nil {
		t.Fatalf("RevokeBiscuit: %v", err)
	}
	if resp.GetCapabilityId() != target.ID.String() {
		t.Errorf("capability_id = %q, want %s", resp.GetCapabilityId(), target.ID)
	}
	if copier.token != "biscuit" {
		t.Errorf("copier read %q, want the request's token", copier.token)
	}
	a := copies.args
	if a == nil {
		t.Fatal("store.RevokeBiscuit was not called")
	}
	if a.Copy.CapabilityID() != target.ID || !bytes.Equal(a.Copy.RevocationID(), copied.RevocationID()) ||
		a.Note != "leaked" || a.Actor != "operator" {
		t.Errorf("args = %+v", a)
	}
	// A platform admin acts on the capability's own tenant, or RLS hides it.
	if acting, ok := auth.ActingTenant(copies.ctx); !ok || acting != owner {
		t.Errorf("listed acting on %v (set=%v), want the owner %v", acting, ok, owner)
	}
}

// A repeated revoke keeps the first entry, and the answer says so with that
// entry, as Revoke's does for a capability.
func TestRevokeBiscuitSaysWhatStands(t *testing.T) {
	const firstReason, firstActor = "leaked first", "first.operator"
	first := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	for _, already := range []bool{false, true} {
		target, copied := mintCopy(t, uuid.New(), limes.Capability{})
		store := &lookupStore{recordingStore: recordingStore{fakeStore: fakeStore{cap: &target}}}
		copies := &copyStore{res: limes.RevokeBiscuitResult{
			AlreadyRevoked: already,
			Revocation:     limes.BiscuitRevocation{RevokedAt: first, Note: firstReason, Actor: firstActor},
		}}
		h := mkHandler(t, store, nil, &allowAuthorizer{}).WithBiscuitCopies(&fakeCopier{copy: copied}, copies)
		resp, err := h.RevokeBiscuit(callerCtx(uuid.New(), "platform.admin"), revokeBiscuitReq())
		if err != nil {
			t.Fatal(err)
		}
		r := resp.GetRevocation()
		if resp.GetAlreadyRevoked() != already || !r.GetRevokedAt().AsTime().Equal(first) ||
			r.GetReason() != firstReason || r.GetActor() != firstActor || r.GetCascade() {
			t.Errorf("already %t: answered %v", already, resp)
		}
	}
}

func TestRevokeBiscuitRefusals(t *testing.T) {
	_, good := mintCopy(t, uuid.New(), limes.Capability{})
	cases := map[string]struct {
		authz  cedar.Authorizer
		copier BiscuitCopier
		copies *copyStore
		want   connect.Code
	}{
		"Cedar denies":           {&denyAuthorizer{}, &fakeCopier{copy: good}, &copyStore{}, connect.CodePermissionDenied},
		"not wired":              {&allowAuthorizer{}, nil, nil, connect.CodeUnavailable},
		"not a genuine Biscuit":  {&allowAuthorizer{}, &fakeCopier{err: limes.ErrInvalidToken}, &copyStore{}, connect.CodeInvalidArgument},
		"capability not visible": {&allowAuthorizer{}, &fakeCopier{copy: good}, &copyStore{err: limes.ErrNotFound}, connect.CodeNotFound},
		"store fails":            {&allowAuthorizer{}, &fakeCopier{copy: good}, &copyStore{err: errors.New("db down")}, connect.CodeInternal},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			store := &recordingStore{}
			h := mkHandler(t, store, nil, tc.authz)
			if tc.copies != nil {
				h.WithBiscuitCopies(tc.copier, tc.copies)
			}
			_, err := h.RevokeBiscuit(callerCtx(uuid.New()), revokeBiscuitReq())
			if codeOf(err) != tc.want {
				t.Fatalf("code = %v, want %v (%v)", codeOf(err), tc.want, err)
			}
			if tc.want == connect.CodePermissionDenied && tc.copies.args != nil {
				t.Error("a denied revoke must not reach the store")
			}
		})
	}
}
