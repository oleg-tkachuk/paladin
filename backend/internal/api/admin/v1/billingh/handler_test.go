package billingh

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/auth"
	"github.com/oleg-tkachuk/paladin/internal/policy/cedar"
)

// allowAuthorizer mirrors the audith / quotah pattern: every Cedar
// check returns Allow. Tests that want to exercise the deny path
// build a denyAuthorizer instead.
type allowAuthorizer struct{}

func (allowAuthorizer) IsAuthorized(_ context.Context, _ *cedar.Principal, _ string, _ *cedar.Resource, _ cedar.RequestContext) (cedar.Decision, error) {
	return cedar.DecisionAllow, nil
}

type denyAuthorizer struct{}

func (denyAuthorizer) IsAuthorized(_ context.Context, _ *cedar.Principal, _ string, _ *cedar.Resource, _ cedar.RequestContext) (cedar.Decision, error) {
	return cedar.DecisionDeny, nil
}

func ctxWithAdmin(t *testing.T) context.Context {
	t.Helper()
	return auth.WithPrincipal(context.Background(), &auth.Principal{
		TenantID: uuid.New(),
		Subject:  "ops@example.com",
		Roles:    []string{"platform.admin"},
		Audience: "paladin-admin",
	})
}

// TestNewHandler_PolicyRequired guards against accidental nil-policy
// wiring — the handler panics so the bug surfaces at startup, not on
// the first request.
func TestNewHandler_PolicyRequired(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Error("expected panic on nil policy")
		}
	}()
	_ = NewHandler(nil, nil, nil)
}

// TestGetTenantSummary_NilPool returns Unavailable when the
// capability subsystem is disabled (deps.Capability == nil → no
// pool wired into the billing handler).
func TestGetTenantSummary_NilPool(t *testing.T) {
	h := NewHandler(nil, nil, allowAuthorizer{})
	_, err := h.GetTenantSummary(ctxWithAdmin(t), uuid.New(), time.Time{}, time.Time{})
	if err == nil {
		t.Fatal("expected error")
	}
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Errorf("code = %v, want Unavailable", connect.CodeOf(err))
	}
}

// TestGetTenantTimeSeries_NilPool same shape as the summary case.
func TestGetTenantTimeSeries_NilPool(t *testing.T) {
	h := NewHandler(nil, nil, allowAuthorizer{})
	_, err := h.GetTenantTimeSeries(ctxWithAdmin(t), uuid.New(), time.Time{}, time.Time{}, "day")
	if err == nil {
		t.Fatal("expected error")
	}
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Errorf("code = %v, want Unavailable", connect.CodeOf(err))
	}
}

// TestResolvePeriod covers the default-lookback + bounds-check paths.
// resolvePeriod is called by both RPC entry points so coverage here
// is enough.
func TestResolvePeriod(t *testing.T) {
	now := time.Now().UTC()

	// Both empty → 30d window ending now.
	s, e, err := resolvePeriod(time.Time{}, time.Time{})
	if err != nil {
		t.Fatalf("default: %v", err)
	}
	if d := e.Sub(s); d < 29*24*time.Hour || d > 31*24*time.Hour {
		t.Errorf("default window = %v, want ~30d", d)
	}

	// start > end → InvalidArgument-shaped error (the handler wraps
	// this into connect.CodeInvalidArgument).
	if _, _, err := resolvePeriod(now, now.Add(-time.Hour)); err == nil {
		t.Error("expected error for start > end")
	}

	// Only start set → end becomes now.
	startOnly := now.Add(-time.Hour)
	s2, e2, err := resolvePeriod(startOnly, time.Time{})
	if err != nil {
		t.Fatalf("start-only: %v", err)
	}
	if !s2.Equal(startOnly) {
		t.Errorf("start = %v, want %v", s2, startOnly)
	}
	if e2.IsZero() {
		t.Error("end should default to now")
	}
}

// TestGetTenantTimeSeries_GranularityValidation rejects values
// outside {hour, day, week} before reaching the SQL. We can't reach
// the SQL anyway (nil pool path returns Unavailable first), so this
// test confirms the order: validation runs before the
// capability-disabled short-circuit ONLY when the value is provided
// AND auth passes. To isolate, we drive a deny authorizer to
// confirm Cedar is the first gate.
func TestGetTenantTimeSeries_DenyFirst(t *testing.T) {
	h := NewHandler(nil, nil, denyAuthorizer{})
	_, err := h.GetTenantTimeSeries(ctxWithAdmin(t), uuid.New(), time.Time{}, time.Time{}, "minute")
	if err == nil {
		t.Fatal("expected error")
	}
	// PermissionDenied beats InvalidArgument because authorize runs
	// before granularity validation in the handler. If this flips,
	// re-order the handler to keep the security gate first.
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("code = %v, want PermissionDenied", connect.CodeOf(err))
	}
}

// TestGetTenantSummary_DenyFirst parallel to the time-series case.
func TestGetTenantSummary_DenyFirst(t *testing.T) {
	h := NewHandler(nil, nil, denyAuthorizer{})
	_, err := h.GetTenantSummary(ctxWithAdmin(t), uuid.New(), time.Time{}, time.Time{})
	if err == nil {
		t.Fatal("expected error")
	}
	if connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Errorf("code = %v, want PermissionDenied", connect.CodeOf(err))
	}
}
