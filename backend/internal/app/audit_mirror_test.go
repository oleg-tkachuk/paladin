package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin-private/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin-private/internal/worker"
)

// TestAuditMirror_EmitsAuditEvent pins the producer-wiring contract: an audit
// entry fans out exactly one paladin.audit.<action> event carrying the tenant,
// the resource name, the actor, and the audit payload (entry id, action,
// audience, request id, source ip, capability id, error message).
func TestAuditMirror_EmitsAuditEvent(t *testing.T) {
	fake := &fakeEventDispatcher{}
	m := &auditMirror{dispatcher: fake, log: zap.NewNop()}

	tenant := uuid.New()
	entryID := uuid.New()
	capID := uuid.New()
	entry := admindomain.AuditEntry{
		EntryID:       entryID,
		At:            time.Now().UTC(),
		ActorSubject:  "admin@local",
		ActorTenantID: tenant,
		ActorAudience: "paladin-admin",
		Action:        "/paladin.admin.v1.TenantService/CreateTenant",
		ResourceName:  "tenants/" + tenant.String(),
		RequestID:     "req-1",
		SourceIP:      "10.0.0.1",
		CapabilityID:  capID,
		ErrorMessage:  "",
	}
	if err := m.EmitAuditedTx(context.Background(), nil, entry); err != nil {
		t.Fatalf("EmitAuditedTx: %v", err)
	}

	if len(fake.calls) != 1 {
		t.Fatalf("Dispatch calls = %d, want 1", len(fake.calls))
	}
	c := fake.calls[0]
	if c.tenantID != tenant.String() {
		t.Errorf("Dispatch tenantID = %q, want %q", c.tenantID, tenant.String())
	}
	if c.evt.Type != "paladin.audit.create_tenant" {
		t.Errorf("event type = %q, want paladin.audit.create_tenant", c.evt.Type)
	}
	if c.evt.ResourceName != entry.ResourceName {
		t.Errorf("resource name = %q, want %q", c.evt.ResourceName, entry.ResourceName)
	}
	if c.evt.ActorSubject != "admin@local" {
		t.Errorf("actor = %q, want admin@local", c.evt.ActorSubject)
	}
	for k, want := range map[string]any{
		"audit_entry_id": entryID.String(),
		"action":         entry.Action,
		"audience":       "paladin-admin",
		"resource":       entry.ResourceName,
		"request_id":     "req-1",
		"source_ip":      "10.0.0.1",
		"capability_id":  capID.String(),
		"error_message":  "",
		"severity":       "info", // successful audit (no error_message)
	} {
		if got := c.evt.Payload[k]; got != want {
			t.Errorf("payload[%q] = %v, want %v", k, got, want)
		}
	}
}

// TestAuditMirror_ErrorStampsWarningSeverity: an audited call that failed
// (non-empty error_message) is stamped severity=warning, which the dispatcher's
// classifyEvent payload-override then elevates from the default info — so a
// subscriber can filter `severity_level >= 30` for failed operations.
func TestAuditMirror_ErrorStampsWarningSeverity(t *testing.T) {
	fake := &fakeEventDispatcher{}
	m := &auditMirror{dispatcher: fake, log: zap.NewNop()}
	tenant := uuid.New()
	if err := m.EmitAuditedTx(context.Background(), nil, admindomain.AuditEntry{
		EntryID:       uuid.New(),
		At:            time.Now().UTC(),
		ActorTenantID: tenant,
		Action:        "/paladin.admin.v1.TenantService/DeleteTenant",
		ResourceName:  "tenants/" + tenant.String(),
		ErrorMessage:  "denied by policy",
	}); err != nil {
		t.Fatalf("EmitAuditedTx: %v", err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("Dispatch calls = %d, want 1", len(fake.calls))
	}
	if got := fake.calls[0].evt.Payload["severity"]; got != "warning" {
		t.Errorf("payload[severity] = %v, want warning (failed audit)", got)
	}
}

// TestAuditMirror_DropsTenantless: a tenant-less entry (pre-auth / platform
// call like Login) has no fan-out target and must be dropped.
func TestAuditMirror_DropsTenantless(t *testing.T) {
	fake := &fakeEventDispatcher{}
	m := &auditMirror{dispatcher: fake, log: zap.NewNop()}
	if err := m.EmitAuditedTx(context.Background(), nil, admindomain.AuditEntry{
		EntryID:       uuid.New(),
		ActorTenantID: uuid.Nil,
		Action:        "/paladin.iam.v1.AuthService/Login",
	}); err != nil {
		t.Fatalf("EmitAuditedTx: %v", err)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("tenant-less entry: Dispatch calls = %d, want 0 (dropped)", len(fake.calls))
	}
}

// TestAuditMirror_PropagatesDispatchError: the transactional fan-out is NOT
// best-effort — a DispatchTx failure must propagate so the audit row rolls
// back with it (no audit-row-without-event window). The interceptor keeps the
// RPC best-effort by swallowing the returned error at its own boundary.
func TestAuditMirror_PropagatesDispatchError(t *testing.T) {
	fake := &fakeEventDispatcher{err: errors.New("outbox down")}
	m := &auditMirror{dispatcher: fake, log: zap.NewNop()}
	err := m.EmitAuditedTx(context.Background(), nil, admindomain.AuditEntry{
		EntryID:       uuid.New(),
		ActorTenantID: uuid.New(),
		Action:        "/paladin.admin.v1.TenantService/CreateTenant",
	})
	if err == nil {
		t.Fatal("EmitAuditedTx returned nil, want the dispatch error to propagate")
	}
	if len(fake.calls) != 1 {
		t.Fatalf("Dispatch should still be attempted once; got %d", len(fake.calls))
	}
}

// TestAuditEventType pins the action → event-class derivation subscribers
// route on: strip the connect-rpc service path, snake_case the method,
// prepend paladin.audit. Includes the degenerate inputs the helper must not
// panic on.
func TestAuditEventType(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"/paladin.admin.v1.TenantService/CreateTenant", "paladin.audit.create_tenant"},
		{"/paladin.data.v1.ObjectService/CompleteUpload", "paladin.audit.complete_upload"},
		{"/paladin.iam.v1.AuthService/Login", "paladin.audit.login"},
		{"CreateBucket", "paladin.audit.create_bucket"}, // no service path
		{"", "paladin.audit."},      // degenerate: empty action
		{"/svc/", "paladin.audit."}, // degenerate: trailing slash
	}
	for _, tc := range cases {
		if got := auditEventType(tc.in); got != tc.want {
			t.Errorf("auditEventType(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestAuditMirrorWiring covers the nil-safety + toggle gating the wiring
// sites rely on: events disabled → nil emitter; enabled but no dispatcher →
// nil; enabled with a dispatcher → live emitter.
func TestAuditMirrorWiring(t *testing.T) {
	d := &worker.Dispatcher{} // non-nil; never dispatched against here

	if got := newAuditMirror(nil, zap.NewNop()); got != nil {
		t.Errorf("newAuditMirror(nil) = %v, want nil", got)
	}
	if got := optionalAuditMirror(false, d, zap.NewNop()); got != nil {
		t.Errorf("optionalAuditMirror(disabled) = %v, want nil", got)
	}
	if got := optionalAuditMirror(true, nil, zap.NewNop()); got != nil {
		t.Errorf("optionalAuditMirror(enabled, nil dispatcher) = %v, want nil", got)
	}
	if got := optionalAuditMirror(true, d, zap.NewNop()); got == nil {
		t.Error("optionalAuditMirror(enabled, dispatcher) = nil, want a live emitter")
	}
}
