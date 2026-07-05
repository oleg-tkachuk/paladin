package cedar

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestIsAuthorized_EvalErrorFailsClosed: a forbid that references a missing
// attribute ERRORS at evaluation and is SKIPPED by cedar — so an unconditional
// permit would otherwise yield ALLOW even though the forbid should have matched.
// The engine must fail closed (a policy-eval error can never become an accidental
// allow) and record it via EvalErrs so the broken policy is observable.
func TestIsAuthorized_EvalErrorFailsClosed(t *testing.T) {
	tid := uuid.New()
	r := &Resource{TenantID: tid, ObjectKey: "invoices", BackendID: "primary", BucketName: "b1"}
	const policy = `permit(principal, action, resource);
forbid(principal, action, resource) when { resource.definitely_missing == "boom" };`

	e := NewEngine(fakeStore{text: policy}, time.Minute)
	dec, err := e.IsAuthorized(context.Background(),
		&Principal{Subject: "u@acme", TenantID: tid, Roles: []string{"tenant.user"}},
		ActionManageObjectKey, r, RequestContext{})
	if err != nil {
		t.Fatalf("IsAuthorized: %v", err)
	}
	if dec != DecisionDeny {
		t.Fatalf("decision = %v, want DecisionDeny — a skipped-forbid eval error must fail closed, not allow", dec)
	}
	if e.EvalErrs() != 1 {
		t.Errorf("EvalErrs() = %d, want 1", e.EvalErrs())
	}
}

// TestIsAuthorized_CleanEvalNoEvalErr guards against over-triggering the
// fail-closed guard: a clean unconditional permit stays Allow and never touches
// the eval-error counter.
func TestIsAuthorized_CleanEvalNoEvalErr(t *testing.T) {
	tid := uuid.New()
	r := &Resource{TenantID: tid, ObjectKey: "invoices", BackendID: "primary", BucketName: "b1"}

	e := NewEngine(fakeStore{text: `permit(principal, action, resource);`}, time.Minute)
	dec, err := e.IsAuthorized(context.Background(),
		&Principal{Subject: "u@acme", TenantID: tid, Roles: []string{"tenant.user"}},
		ActionManageObjectKey, r, RequestContext{})
	if err != nil {
		t.Fatalf("IsAuthorized: %v", err)
	}
	if dec != DecisionAllow {
		t.Fatalf("decision = %v, want DecisionAllow", dec)
	}
	if e.EvalErrs() != 0 {
		t.Errorf("EvalErrs() = %d, want 0 (clean eval must not count)", e.EvalErrs())
	}
}
