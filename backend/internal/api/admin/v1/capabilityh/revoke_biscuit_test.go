package capabilityh

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
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
	err  error
}

func (s *copyStore) IsBiscuitRevoked(context.Context, [][]byte) (bool, error) { return false, nil }
func (s *copyStore) GetBiscuitRevocation(context.Context, []byte) (limes.BiscuitRevocation, error) {
	return limes.BiscuitRevocation{}, limes.ErrNotFound
}
func (s *copyStore) RevokeBiscuit(ctx context.Context, args limes.RevokeBiscuitRequest) error {
	s.args, s.ctx = &args, ctx
	return s.err
}

func revokeBiscuitReq() *adminv1.CapabilityServiceRevokeBiscuitRequest {
	return &adminv1.CapabilityServiceRevokeBiscuitRequest{Token: "biscuit", Reason: "leaked"}
}

func TestRevokeBiscuitListsTheCopy(t *testing.T) {
	owner := uuid.New()
	target := mkParent(owner, limes.OpGet)
	store := &lookupStore{recordingStore: recordingStore{fakeStore: fakeStore{cap: &target}}}
	copier := &fakeCopier{copy: limes.BiscuitCopy{CapabilityID: target.ID, RevocationID: []byte("last-block")}}
	copies := &copyStore{}
	h := NewHandler(mkIssuer(t, &store.fakeStore), store, nil, &allowAuthorizer{}).WithBiscuitCopies(copier, copies)

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
	if a.CapabilityID != target.ID || !bytes.Equal(a.RevocationID, []byte("last-block")) ||
		a.Reason != "leaked" || a.Actor != "operator" {
		t.Errorf("args = %+v", a)
	}
	// A platform admin acts on the capability's own tenant, or RLS hides it.
	if acting, ok := auth.ActingTenant(copies.ctx); !ok || acting != owner {
		t.Errorf("listed acting on %v (set=%v), want the owner %v", acting, ok, owner)
	}
}

func TestRevokeBiscuitRefusals(t *testing.T) {
	id := uuid.New()
	good := limes.BiscuitCopy{CapabilityID: id, RevocationID: []byte("x")}
	cases := map[string]struct {
		authz  cedar.Authorizer
		copier BiscuitCopier
		copies *copyStore
		want   connect.Code
	}{
		"Cedar denies":           {&denyAuthorizer{}, &fakeCopier{copy: good}, &copyStore{}, connect.CodePermissionDenied},
		"not wired":              {&allowAuthorizer{}, nil, nil, connect.CodeUnavailable},
		"not a genuine Biscuit":  {&allowAuthorizer{}, &fakeCopier{err: limes.ErrInvalidSignature}, &copyStore{}, connect.CodeInvalidArgument},
		"capability not visible": {&allowAuthorizer{}, &fakeCopier{copy: good}, &copyStore{err: limes.ErrNotFound}, connect.CodeNotFound},
		"store fails":            {&allowAuthorizer{}, &fakeCopier{copy: good}, &copyStore{err: errors.New("db down")}, connect.CodeInternal},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			store := &recordingStore{}
			h := NewHandler(mkIssuer(t, &store.fakeStore), store, nil, tc.authz)
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
