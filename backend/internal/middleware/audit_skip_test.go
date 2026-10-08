package middleware

import (
	"testing"

	"connectrpc.com/connect/v2"
)

// The audit log is the trail of mutations. A read is skipped when the contract
// says it has no side effects, whatever its name, or when it is named like one;
// session operations are skipped because every console page load runs them.
func TestAuditSkipsReadsAndSessionOperations(t *testing.T) {
	a := &auditInterceptor{}
	for name, tc := range map[string]struct {
		spec connect.Spec
		skip bool
	}{
		"a read the contract declares, named like a verb": {
			connect.Spec{Procedure: "/paladin.admin.v1.TenantBudgetService/Summarize", IdempotencyLevel: connect.IdempotencyNoSideEffects}, true},
		"an export the contract declares": {
			connect.Spec{Procedure: "/paladin.admin.v1.AuditService/ExportAuditLog", IdempotencyLevel: connect.IdempotencyNoSideEffects}, true},
		"a read declaring nothing, named like one": {
			connect.Spec{Procedure: "/paladin.admin.v1.BackendService/GetBackend"}, true},
		"audience exchange": {
			connect.Spec{Procedure: "/paladin.iam.v1.AuthService/ExchangeAudience"}, true},
		"login": {
			connect.Spec{Procedure: "/paladin.iam.v1.AuthService/Login"}, true},
		"an idempotent mutation": {
			connect.Spec{Procedure: "/paladin.admin.v1.CapabilityService/Revoke", IdempotencyLevel: connect.IdempotencyIdempotent}, false},
		"a mutation declaring nothing": {
			connect.Spec{Procedure: "/paladin.admin.v1.TenantService/CreateTenant"}, false},
		"a mutation whose name merely contains a read verb": {
			connect.Spec{Procedure: "/paladin.admin.v1.TenantService/PurgeTenant"}, false},
	} {
		if got := a.shouldSkip(tc.spec); got != tc.skip {
			t.Errorf("%s: shouldSkip = %v, want %v", name, got, tc.skip)
		}
	}

	// recordReads keeps everything, reads included.
	all := &auditInterceptor{recordReads: true}
	if all.shouldSkip(connect.Spec{Procedure: "/x.Y/Summarize", IdempotencyLevel: connect.IdempotencyNoSideEffects}) {
		t.Error("recordReads skipped a read")
	}
}
