package audith

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
)

// GetAuditLogEntry was one of the thirteen handlers at 0.0% across unit and
// both integration suites (BACKLOG: "Half the admin API's RPCs have no
// behavioural test").
//
// Its cross-tenant answer is the part worth holding still: a tenant admin who
// asks for another tenant's entry gets NotFound, not PermissionDenied. That is
// deliberate — PermissionDenied would confirm the entry exists, and an audit
// log is precisely where "does this record exist" is itself the secret. The
// distinction is one line in the handler and invisible from the outside unless
// a test names it.

// denyAuthorizer refuses everything, so a test can show that owning the
// tenant is necessary but not sufficient.
type denyAuthorizer struct{}

func (denyAuthorizer) IsAuthorized(context.Context, *cedar.Principal, cedar.Action, *cedar.Resource, cedar.RequestContext) (cedar.Decision, error) {
	return cedar.DecisionDeny, nil
}

type oneEntryRepo struct {
	fakeAuditRepo
	entry admindomain.AuditEntry
	err   error
}

func (r *oneEntryRepo) Get(context.Context, uuid.UUID) (admindomain.AuditEntry, error) {
	if r.err != nil {
		return admindomain.AuditEntry{}, r.err
	}
	return r.entry, nil
}

func ctxAsTenantAdmin(tenantID uuid.UUID) context.Context {
	return auth.WithPrincipal(context.Background(), &auth.Principal{
		TenantID: tenantID,
		Subject:  "tenant-admin@example.com",
		Roles:    []string{"tenant.admin"},
		Audience: "paladin-admin",
	})
}

func entryFor(tenantID uuid.UUID) admindomain.AuditEntry {
	return admindomain.AuditEntry{
		EntryID:       uuid.Must(uuid.NewV7()),
		At:            time.Unix(1700, 0).UTC(),
		ActorSubject:  "alice",
		ActorTenantID: tenantID,
		ActorAudience: "paladin-admin",
		Action:        "/paladin.admin.v1.BucketService/UpdateBucket",
	}
}

func TestGetAuditLogEntryReturnsTheCallersOwnEntry(t *testing.T) {
	tenantID := uuid.New()
	e := entryFor(tenantID)
	h := NewHandler(&oneEntryRepo{entry: e}, allowAuthorizer{})

	got, err := h.GetAuditLogEntry(ctxAsTenantAdmin(tenantID), e.EntryID)
	if err != nil {
		t.Fatalf("GetAuditLogEntry: %v", err)
	}
	if got.EntryID != e.EntryID || got.Action != e.Action {
		t.Errorf("got %+v, want the stored entry", got)
	}
}

func TestGetAuditLogEntryHidesAnotherTenantsEntryAsNotFound(t *testing.T) {
	// The entry belongs to someone else. NotFound rather than PermissionDenied
	// is the whole point: an audit log is where the existence of a record is
	// itself information about the other tenant.
	h := NewHandler(&oneEntryRepo{entry: entryFor(uuid.New())}, allowAuthorizer{})

	_, err := h.GetAuditLogEntry(ctxAsTenantAdmin(uuid.New()), uuid.New())
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound — PermissionDenied would confirm the entry exists", connect.CodeOf(err))
	}
}

func TestGetAuditLogEntryLetsPlatformAdminCrossTenants(t *testing.T) {
	// The same read that is hidden from a tenant admin is the platform
	// admin's job.
	e := entryFor(uuid.New())
	h := NewHandler(&oneEntryRepo{entry: e}, allowAuthorizer{})

	got, err := h.GetAuditLogEntry(ctxWithPlatformAdmin(t), e.EntryID)
	if err != nil {
		t.Fatalf("platform.admin was refused a cross-tenant entry: %v", err)
	}
	if got.EntryID != e.EntryID {
		t.Errorf("got %v, want %v", got.EntryID, e.EntryID)
	}
}

func TestGetAuditLogEntryAbsentEntryIsNotFound(t *testing.T) {
	h := NewHandler(&oneEntryRepo{err: admindomain.ErrNotFound}, allowAuthorizer{})

	_, err := h.GetAuditLogEntry(ctxWithPlatformAdmin(t), uuid.New())
	if connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("code = %v, want NotFound", connect.CodeOf(err))
	}
}

func TestGetAuditLogEntryDeniedByCedar(t *testing.T) {
	tenantID := uuid.New()
	h := NewHandler(&oneEntryRepo{entry: entryFor(tenantID)}, denyAuthorizer{})

	_, err := h.GetAuditLogEntry(ctxAsTenantAdmin(tenantID), uuid.New())
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatalf("code = %v, want PermissionDenied — owning the tenant is not the only gate", connect.CodeOf(err))
	}
}
