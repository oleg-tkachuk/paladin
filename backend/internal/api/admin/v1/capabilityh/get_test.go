package capabilityh

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar/cedartest"
	"github.com/oleg-tkachuk/paladin/capability"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// recordStore answers GetRecord with rec, or err, and records the context.
type recordStore struct {
	fakeStore
	rec    capability.Record
	err    error
	getCtx context.Context
}

func (s *recordStore) GetRecord(ctx context.Context, id uuid.UUID) (capability.Record, error) {
	s.getCtx = ctx
	if s.err != nil {
		return capability.Record{}, s.err
	}
	if id != s.rec.Capability.ID {
		return capability.Record{}, capability.ErrNotFound
	}
	return s.rec, nil
}

func getReq(id string) *adminv1.CapabilityServiceGetRequest {
	return &adminv1.CapabilityServiceGetRequest{Id: id}
}

func TestGetReturnsTheRecord(t *testing.T) {
	owner := uuid.New()
	c := mkParent(owner, capability.OpGet)
	revokedAt := time.Unix(1_800_000_000, 0).UTC()
	issuer := capability.Principal{Type: capability.PrincipalUser, TenantID: owner, Subject: "operator"}
	store := &recordStore{rec: capability.Record{
		Capability: c, IssuedBy: issuer,
		Revocation: &capability.Revocation{RevokedAt: revokedAt, Reason: "leak", Actor: "sec", Cascade: true},
	}}
	h := NewHandler(mkIssuer(t, &store.fakeStore), store, nil, &allowAuthorizer{})

	resp, err := h.Get(adminCtx(), getReq(c.ID.String()))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	m := resp
	if m.GetCapability().GetId() != c.ID.String() || m.GetIssuedBy().GetSubject() != "operator" ||
		m.GetIssuedBy().GetKind() != adminv1.PrincipalKind_PRINCIPAL_KIND_USER {
		t.Errorf("capability %v issued by %v; want %s issued by the operator", m.GetCapability().GetId(), m.GetIssuedBy(), c.ID)
	}
	r := m.GetRevocation()
	if r == nil || r.GetReason() != "leak" || r.GetActor() != "sec" || !r.GetCascade() || !r.GetRevokedAt().AsTime().Equal(revokedAt) {
		t.Errorf("revocation = %v; want the record's", r)
	}

	store.rec.Revocation = nil
	resp, err = h.Get(adminCtx(), getReq(c.ID.String()))
	if err != nil || resp.GetRevocation() != nil {
		t.Errorf("a capability with no entry of its own = %v, %v; want no revocation", resp.GetRevocation(), err)
	}
}

func TestGetErrors(t *testing.T) {
	c := mkParent(uuid.New(), capability.OpGet)
	cases := []struct {
		name  string
		store *recordStore
		authz cedar.Authorizer
		id    string
		want  connect.Code
	}{
		{"unknown id", &recordStore{rec: capability.Record{Capability: c}}, &allowAuthorizer{}, uuid.New().String(), connect.CodeNotFound},
		{"malformed id", &recordStore{}, &allowAuthorizer{}, "nope", connect.CodeInvalidArgument},
		{"store failure", &recordStore{err: errors.New("db down")}, &allowAuthorizer{}, c.ID.String(), connect.CodeInternal},
		{"denied", &recordStore{rec: capability.Record{Capability: c}}, &denyAuthorizer{}, c.ID.String(), connect.CodePermissionDenied},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := NewHandler(mkIssuer(t, &tc.store.fakeStore), tc.store, nil, tc.authz)
			_, err := h.Get(adminCtx(), getReq(tc.id))
			if codeOf(err) != tc.want {
				t.Fatalf("code = %v, want %v", codeOf(err), tc.want)
			}
		})
	}
}

// A platform admin reads another tenant's capability on that tenant's
// connection, as Revoke and GetUsage do; RLS would hide it otherwise.
func TestGetActsOnTheCapabilitysTenant(t *testing.T) {
	owner := uuid.New()
	c := mkParent(owner, capability.OpGet)
	store := &recordStore{fakeStore: fakeStore{cap: &c}, rec: capability.Record{Capability: c}}
	h := NewHandler(mkIssuer(t, &store.fakeStore), store, nil, cedartest.Engine(""))

	if _, err := h.Get(callerCtx(uuid.New(), "platform.admin"), getReq(c.ID.String())); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if acting, ok := auth.ActingTenant(store.getCtx); !ok || acting != owner {
		t.Errorf("GetRecord ran acting on %v (set=%v), want the owner %v", acting, ok, owner)
	}
}
