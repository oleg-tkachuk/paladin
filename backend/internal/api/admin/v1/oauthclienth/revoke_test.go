package oauthclienth

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	oauthpkg "github.com/oleg-tkachuk/paladin/backend/internal/auth/oauth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
)

// fakeRevoker records each revocation, whether it was scoped to write every
// tenant, and what the client store had done by then.
type fakeRevoker struct {
	clients     *fakeClients
	calls       []limes.RevokeMatchingRequest
	everyTenant []bool
	deletedBy   []int
	err         error
}

func (f *fakeRevoker) RevokeMatching(ctx context.Context, req limes.RevokeMatchingRequest) (limes.RevokeMatchingResult, error) {
	f.calls = append(f.calls, req)
	f.everyTenant = append(f.everyTenant, auth.AllTenantsWrite(ctx))
	f.deletedBy = append(f.deletedBy, len(f.clients.deleted))
	return limes.RevokeMatchingResult{Revoked: 1}, f.err
}

func revokingHandler(clients *fakeClients, revoker *fakeRevoker) *Handler {
	revoker.clients = clients
	return newHandler(clients, &fakeAuthorizer{decision: cedar.DecisionAllow}).
		WithCapabilityRevocation(&auth.BulkRevocation{Revoker: revoker})
}

// Deleting a client revokes every capability exchanged under it, in every
// tenant, as the admin who deleted it, once the client is gone; before,
// they outlived it until their tokens expired.
func TestDeleteRevokesTheClientsCapabilities(t *testing.T) {
	clients, revoker := &fakeClients{}, &fakeRevoker{}
	if err := del(revokingHandler(clients, revoker), ctxAs(apiutil.RolePlatformAdmin), registered); err != nil {
		t.Fatal(err)
	}
	if len(revoker.calls) != 1 {
		t.Fatalf("%d revocations, want 1", len(revoker.calls))
	}
	req := revoker.calls[0]
	if !req.AllTenants || req.ClientID != registered || req.Actor != "tester" || req.Note == "" {
		t.Errorf("request = %+v, want client %s in every tenant, by tester, with a note", req, registered)
	}
	if !revoker.everyTenant[0] {
		t.Error("the revocation was not scoped to write every tenant")
	}
	if revoker.deletedBy[0] != 1 {
		t.Error("revoked before the client was deleted: a capability could be exchanged meanwhile")
	}
}

// A client already gone — a retry after its capabilities failed to go —
// still has them revoked, then answers NotFound.
func TestDeleteRevokesForAClientAlreadyGone(t *testing.T) {
	clients, revoker := &fakeClients{err: oauthpkg.ErrClientNotFound}, &fakeRevoker{}
	err := del(revokingHandler(clients, revoker), ctxAs(apiutil.RolePlatformAdmin), registered)
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound", connect.CodeOf(err))
	}
	if len(revoker.calls) != 1 {
		t.Errorf("%d revocations, want 1", len(revoker.calls))
	}
}

// The revocation failing fails the delete, to be retried; a refused delete
// revokes nothing.
func TestDeleteRevocationFailures(t *testing.T) {
	failing := &fakeRevoker{err: errors.New("pgstore: connection refused")}
	if err := del(revokingHandler(&fakeClients{}, failing), ctxAs(apiutil.RolePlatformAdmin), registered); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Errorf("revocation failing: code = %v, want Unavailable", connect.CodeOf(err))
	}
	for name, ctx := range map[string]context.Context{
		"a seeded client": ctxAs(apiutil.RolePlatformAdmin),
		"a tenant admin":  ctxAs("tenant.admin"),
	} {
		revoker := &fakeRevoker{}
		id := registered
		if name == "a seeded client" {
			id = seeded
		}
		if err := del(revokingHandler(&fakeClients{}, revoker), ctx, id); err == nil || len(revoker.calls) != 0 {
			t.Errorf("%s: err = %v after %d revocations, want a refusal and none", name, err, len(revoker.calls))
		}
	}
}
