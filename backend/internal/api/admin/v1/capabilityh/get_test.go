package capabilityh

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar/cedartest"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// recordStore answers GetRecord with rec, or err, and records the context.
type recordStore struct {
	fakeStore
	rec    limes.Record
	err    error
	getCtx context.Context
	// revoked and revokedErr answer IsRevoked, as for c or an ancestor.
	revoked    bool
	revokedErr error
}

func (s *recordStore) IsRevoked(context.Context, uuid.UUID) (bool, error) {
	return s.revoked, s.revokedErr
}

func (s *recordStore) GetRecord(ctx context.Context, id uuid.UUID) (limes.Record, error) {
	s.getCtx = ctx
	if s.err != nil {
		return limes.Record{}, s.err
	}
	if id != s.rec.Capability.ID {
		return limes.Record{}, limes.ErrNotFound
	}
	return s.rec, nil
}

func getReq(id string) *adminv1.CapabilityServiceGetRequest {
	return &adminv1.CapabilityServiceGetRequest{Id: id}
}

func TestGetReturnsTheRecord(t *testing.T) {
	owner := uuid.New()
	c := mkParent(owner, limes.OpGet)
	revokedAt := time.Unix(1_800_000_000, 0).UTC()
	issuer := limes.Principal{Type: limes.PrincipalTypeUser, TenantID: owner, Subject: "operator"}
	store := &recordStore{rec: limes.Record{
		Capability: c, IssuedBy: issuer,
		Revocation: &limes.Revocation{RevokedAt: revokedAt, Note: "leak", Actor: "sec", Cascaded: true},
	}}
	h := mkHandler(t, store, nil, &allowAuthorizer{})

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
	c := mkParent(uuid.New(), limes.OpGet)
	cases := []struct {
		name  string
		store *recordStore
		authz cedar.Authorizer
		id    string
		want  connect.Code
	}{
		{"unknown id", &recordStore{rec: limes.Record{Capability: c}}, &allowAuthorizer{}, uuid.New().String(), connect.CodeNotFound},
		{"malformed id", &recordStore{}, &allowAuthorizer{}, "nope", connect.CodeInvalidArgument},
		{"store failure", &recordStore{err: errors.New("db down")}, &allowAuthorizer{}, c.ID.String(), connect.CodeInternal},
		{"denied", &recordStore{rec: limes.Record{Capability: c}}, &denyAuthorizer{}, c.ID.String(), connect.CodePermissionDenied},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := mkHandler(t, tc.store, nil, tc.authz)
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
	c := mkParent(owner, limes.OpGet)
	store := &recordStore{fakeStore: fakeStore{cap: &c}, rec: limes.Record{Capability: c}}
	h := mkHandler(t, store, nil, cedartest.Engine(""))

	if _, err := h.Get(callerCtx(uuid.New(), "platform.admin"), getReq(c.ID.String())); err != nil {
		t.Fatalf("Get: %v", err)
	}
	if acting, ok := auth.ActingTenant(store.getCtx); !ok || acting != owner {
		t.Errorf("GetRecord ran acting on %v (set=%v), want the owner %v", acting, ok, owner)
	}
}

// Get gives a capability its disposition, as List does: revoked through an
// ancestor too, though the record holds no entry of its own.
func TestGetGivesTheDisposition(t *testing.T) {
	owner := uuid.New()
	live := mkParent(owner, limes.OpGet)
	live.ExpiresAt = time.Now().Add(time.Hour)
	cases := map[string]struct {
		expires time.Time
		revoked bool
		want    adminv1.CapabilityDisposition
	}{
		"active":                     {live.ExpiresAt, false, adminv1.CapabilityDisposition_CAPABILITY_DISPOSITION_ACTIVE},
		"expired":                    {time.Now().Add(-time.Minute), false, adminv1.CapabilityDisposition_CAPABILITY_DISPOSITION_EXPIRED},
		"revoked through its parent": {live.ExpiresAt, true, adminv1.CapabilityDisposition_CAPABILITY_DISPOSITION_REVOKED},
		"revoked and expired":        {time.Now().Add(-time.Minute), true, adminv1.CapabilityDisposition_CAPABILITY_DISPOSITION_REVOKED},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			c := live
			c.ExpiresAt = tc.expires
			h := mkHandler(t, &recordStore{rec: limes.Record{Capability: c}, revoked: tc.revoked}, nil, &allowAuthorizer{})
			resp, err := h.Get(adminCtx(), getReq(c.ID.String()))
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got := resp.GetCapability().GetDisposition(); got != tc.want {
				t.Errorf("disposition = %v, want %v", got, tc.want)
			}
		})
	}
}

// A revocation check that fails is no answer: Get does not guess one.
func TestGetFailsWhenTheRevocationCheckDoes(t *testing.T) {
	c := mkParent(uuid.New(), limes.OpGet)
	store := &recordStore{rec: limes.Record{Capability: c}, revokedErr: errors.New("db down")}
	h := mkHandler(t, store, nil, &allowAuthorizer{})
	if _, err := h.Get(adminCtx(), getReq(c.ID.String())); connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("code = %v, want Internal", connect.CodeOf(err))
	}
}

// The census's rule, at its boundary: an expiry at now is expired, and a
// capability with none counts as expired, not active.
func TestRecordDisposition(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	cases := []struct {
		name    string
		expires time.Time
		revoked bool
		want    limes.RecordDisposition
	}{
		{"after now", now.Add(time.Second), false, limes.RecordDispositionActive},
		{"at now", now, false, limes.RecordDispositionExpired},
		{"no expiry", time.Time{}, false, limes.RecordDispositionExpired},
		{"revoked", now.Add(time.Hour), true, limes.RecordDispositionRevoked},
	}
	for _, tc := range cases {
		if got := recordDisposition(limes.Capability{ExpiresAt: tc.expires}, tc.revoked, now); got != tc.want {
			t.Errorf("%s: %q, want %q", tc.name, got, tc.want)
		}
	}
}
