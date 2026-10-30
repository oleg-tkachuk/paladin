package capabilityh

import (
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/limes"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// sessionRecord is a capability exchanged under an OAuth grant: an agent's,
// for a user, granted to a client, in the grant's session.
func sessionRecord(tenant uuid.UUID) limes.Record {
	c := mkParent(tenant, limes.OpGet)
	c.ClientID = "agent-client"
	c.OnBehalfOf = &limes.Principal{Type: limes.PrincipalTypeUser, TenantID: tenant, Subject: uuid.New().String()}
	return limes.Record{Capability: c, Session: uuid.New().String()}
}

// assertGrant checks that got carries rec's grant: its session, the user it
// acts for and the client it was granted to.
func assertGrant(t *testing.T, got *adminv1.Capability, rec limes.Record) {
	t.Helper()
	if got.GetSession() != rec.Session {
		t.Errorf("session = %q, want %q", got.GetSession(), rec.Session)
	}
	if got.GetOnBehalfOf().GetSubject() != rec.Capability.OnBehalfOf.Subject ||
		got.GetOnBehalfOf().GetKind() != adminv1.PrincipalKind_PRINCIPAL_KIND_USER {
		t.Errorf("on_behalf_of = %v, want user %s", got.GetOnBehalfOf(), rec.Capability.OnBehalfOf.Subject)
	}
	if got.GetClientId() != rec.Capability.ClientID {
		t.Errorf("client_id = %q, want %q", got.GetClientId(), rec.Capability.ClientID)
	}
}

// Get and List carry the OAuth grant a capability was exchanged under, so
// the console can link one to the other.
func TestGetAndListCarryTheGrant(t *testing.T) {
	tenant := uuid.New()
	rec := sessionRecord(tenant)

	got, err := mkHandler(t, &recordStore{rec: rec}, nil, &allowAuthorizer{}).Get(adminCtx(), getReq(rec.Capability.ID.String()))
	if err != nil {
		t.Fatal(err)
	}
	assertGrant(t, got.GetCapability(), rec)

	listed, err := mkHandler(t, &recordingStore{listOut: []limes.RecordListing{{Record: rec}}}, nil, &allowAuthorizer{}).
		List(adminCtx(), &adminv1.CapabilityServiceListRequest{TenantId: tenant.String()})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.GetCapabilities()) != 1 {
		t.Fatalf("listed %d, want 1", len(listed.GetCapabilities()))
	}
	assertGrant(t, listed.GetCapabilities()[0], rec)
}

// A capability issued outside a grant carries none of it.
func TestACapabilityOutsideAGrantCarriesNone(t *testing.T) {
	rec := limes.Record{Capability: mkParent(uuid.New(), limes.OpGet)}
	got, err := mkHandler(t, &recordStore{rec: rec}, nil, &allowAuthorizer{}).Get(adminCtx(), getReq(rec.Capability.ID.String()))
	if err != nil {
		t.Fatal(err)
	}
	if c := got.GetCapability(); c.GetSession() != "" || c.GetOnBehalfOf() != nil {
		t.Errorf("session %q, on_behalf_of %v; want none", c.GetSession(), c.GetOnBehalfOf())
	}
}
