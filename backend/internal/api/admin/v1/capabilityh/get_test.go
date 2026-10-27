package capabilityh

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	"connectrpc.com/connect/v2"

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
	// disposition is what GetRecordListing gives the record, and stoppedBy
	// the entry that revokes it.
	disposition limes.RecordDisposition
	stoppedBy   *limes.CapabilityRevocation
}

// GetRecordListing makes recordStore the handler's record reader: rec, under
// disposition, or err, recording the context as GetRecord does.
func (s *recordStore) GetRecordListing(ctx context.Context, id uuid.UUID) (limes.RecordListing, error) {
	rec, err := s.GetRecord(ctx, id)
	if err != nil {
		return limes.RecordListing{}, err
	}
	return limes.RecordListing{Record: rec, Disposition: s.disposition, StoppedBy: s.stoppedBy}, nil
}

func (s *recordStore) ListRecords(context.Context, limes.ListRecordsRequest) (limes.Page[limes.RecordListing], error) {
	return limes.Page[limes.RecordListing]{}, errors.New("not used")
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

// Get gives a capability the disposition the store reads it under, revoked
// through its parent among them, though the record holds no entry of its own.
func TestGetGivesTheDisposition(t *testing.T) {
	c := mkParent(uuid.New(), limes.OpGet)
	for d, want := range map[limes.RecordDisposition]adminv1.CapabilityDisposition{
		limes.RecordDispositionActive:  adminv1.CapabilityDisposition_CAPABILITY_DISPOSITION_ACTIVE,
		limes.RecordDispositionExpired: adminv1.CapabilityDisposition_CAPABILITY_DISPOSITION_EXPIRED,
		limes.RecordDispositionRevoked: adminv1.CapabilityDisposition_CAPABILITY_DISPOSITION_REVOKED,
	} {
		t.Run(string(d), func(t *testing.T) {
			h := mkHandler(t, &recordStore{rec: limes.Record{Capability: c}, disposition: d}, nil, &allowAuthorizer{})
			resp, err := h.Get(adminCtx(), getReq(c.ID.String()))
			if err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got := resp.GetCapability().GetDisposition(); got != want {
				t.Errorf("disposition = %v, want %v", got, want)
			}
		})
	}
}

// Without a record reader Get says so rather than answer a capability with
// no disposition, which would read as active.
func TestGetUnwiredIsUnavailable(t *testing.T) {
	c := mkParent(uuid.New(), limes.OpGet)
	store := &fakeStore{cap: &c}
	m := newMinter(t, store)
	h := NewHandler(m.Issuer, m.Verifier, store, nil, &allowAuthorizer{})
	if _, err := h.Get(adminCtx(), getReq(c.ID.String())); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("code = %v, want Unavailable", connect.CodeOf(err))
	}
}

// A capability revoked only through an ancestor says which: its record holds
// no entry of its own, and the ancestor's is what stopped it.
func TestGetSaysWhatStoppedIt(t *testing.T) {
	c := mkParent(uuid.New(), limes.OpGet)
	parent := uuid.New()
	revokedAt := time.Unix(1_800_000_000, 0).UTC()
	store := &recordStore{
		rec:         limes.Record{Capability: c},
		disposition: limes.RecordDispositionRevoked,
		stoppedBy: &limes.CapabilityRevocation{
			CapabilityID: parent,
			Revocation:   limes.Revocation{RevokedAt: revokedAt, Note: "leak", Actor: "sec", Cascaded: false},
		},
	}
	h := mkHandler(t, store, nil, &allowAuthorizer{})
	resp, err := h.Get(adminCtx(), getReq(c.ID.String()))
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	by := resp.GetStoppedBy()
	if resp.GetRevocation() != nil || by.GetCapabilityId() != parent.String() || by.GetRevocation().GetReason() != "leak" || !by.GetRevocation().GetRevokedAt().AsTime().Equal(revokedAt) {
		t.Errorf("revocation %v, stopped by %v; want none of its own, stopped by %s", resp.GetRevocation(), by, parent)
	}

	store.disposition, store.stoppedBy = limes.RecordDispositionActive, nil
	if resp, err := h.Get(adminCtx(), getReq(c.ID.String())); err != nil || resp.GetStoppedBy() != nil {
		t.Errorf("an active capability says it was stopped by %v (%v)", resp.GetStoppedBy(), err)
	}
}
