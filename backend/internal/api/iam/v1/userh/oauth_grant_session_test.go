package userh

import (
	"context"
	"errors"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
)

// fakeSessions records each RevokeMatching call and the tenant its context
// acts on, answering a full batch first when batches is above one.
type fakeSessions struct {
	calls   []limes.RevokeMatchingRequest
	tenants []uuid.UUID
	batches int
	err     error
}

func (f *fakeSessions) RevokeMatching(ctx context.Context, req limes.RevokeMatchingRequest) (limes.RevokeMatchingResult, error) {
	f.calls = append(f.calls, req)
	tenant, _ := auth.ActingTenant(ctx)
	f.tenants = append(f.tenants, tenant)
	if f.err != nil {
		return limes.RevokeMatchingResult{}, f.err
	}
	return limes.RevokeMatchingResult{Revoked: 1, More: len(f.calls) < f.batches}, nil
}

// Ending a grant from the console ends the capabilities exchanged under
// its session too, in the user's tenant and as the operator who ended it;
// before, they outlived the grant until their tokens expired.
func TestRevokeOAuthGrantEndsTheSessionsCapabilities(t *testing.T) {
	const batches = 2
	u, g, _, h := grantFixture(t)
	grant := g.grants[0].ID
	g.found = true
	sessions := &fakeSessions{batches: batches}
	h.WithSessionRevoker(sessions)

	if err := h.RevokeOAuthGrant(ctxAs(uuid.New(), "platform.admin"), ref(u), grant); err != nil {
		t.Fatal(err)
	}
	if g.revoked.grant != grant {
		t.Errorf("the grant's refresh tokens were not ended: revoked %v", g.revoked)
	}
	if len(sessions.calls) != batches {
		t.Fatalf("%d RevokeMatching calls, want %d: a full batch is followed by another", len(sessions.calls), batches)
	}
	req := sessions.calls[0]
	if req.Session != grant.String() || req.TenantID != u.TenantID || req.Actor != "admin" || req.Note == "" {
		t.Errorf("request = %+v, want session %s in tenant %s by admin, with a note", req, grant, u.TenantID)
	}
	if sessions.tenants[0] != u.TenantID {
		t.Errorf("acted on tenant %s, want the user's %s", sessions.tenants[0], u.TenantID)
	}
}

// A grant already ended — a retry after its capabilities failed to go —
// still has them revoked, and answers as done.
func TestRevokeOAuthGrantFinishesAnEndedGrant(t *testing.T) {
	u, g, _, h := grantFixture(t)
	g.found = false
	sessions := &fakeSessions{}
	h.WithSessionRevoker(sessions)

	if err := h.RevokeOAuthGrant(ctxAs(u.TenantID), ref(u), g.grants[0].ID); err != nil {
		t.Fatalf("an ended grant of the user's: %v, want done", err)
	}
	if len(sessions.calls) != 1 {
		t.Errorf("%d RevokeMatching calls, want 1", len(sessions.calls))
	}
}

// A grant that is not the user's is not found, and nothing is revoked: not
// its refresh tokens, and not the capabilities of a session the caller was
// never authorized over.
func TestRevokeOAuthGrantTouchesNoOtherGrant(t *testing.T) {
	u, g, _, h := grantFixture(t)
	sessions := &fakeSessions{}
	h.WithSessionRevoker(sessions)

	if err := h.RevokeOAuthGrant(ctxAs(u.TenantID), ref(u), uuid.New()); code(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound", code(err))
	}
	if g.revoked.grant != uuid.Nil() || len(sessions.calls) != 0 {
		t.Errorf("revoked grant %s and %d sessions, want nothing", g.revoked.grant, len(sessions.calls))
	}
}

// The session's capabilities failing to go fails the call, so the operator
// retries; the refresh tokens are ended first, so no new capability can be
// exchanged meanwhile.
func TestRevokeOAuthGrantReportsTheSessionFailing(t *testing.T) {
	u, g, _, h := grantFixture(t)
	g.found = true
	h.WithSessionRevoker(&fakeSessions{err: errors.New("pgstore: connection refused")})

	if err := h.RevokeOAuthGrant(ctxAs(u.TenantID), ref(u), g.grants[0].ID); code(err) != connect.CodeUnavailable {
		t.Fatalf("code = %v, want Unavailable", code(err))
	}
	if g.revoked.grant != g.grants[0].ID {
		t.Error("the refresh tokens were not ended before the session")
	}
}

// With the capability subsystem off there is no session to end; the grant
// still is.
func TestRevokeOAuthGrantWithoutCapabilities(t *testing.T) {
	u, g, _, h := grantFixture(t)
	g.found = true
	if err := h.RevokeOAuthGrant(ctxAs(u.TenantID), ref(u), g.grants[0].ID); err != nil {
		t.Fatal(err)
	}
	if g.revoked.grant != g.grants[0].ID {
		t.Error("the grant was not ended")
	}
}
