package billingh

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
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

// TestGetTenantTimeSeries_RejectsTooManyBuckets: a period ÷ granularity that
// would materialise more than maxTimeSeriesBuckets rows is rejected as
// InvalidArgument, bounding the buffered result set. A sane window passes the
// cap (and then hits the nil-pool Unavailable, proving no false-trip).
func TestGetTenantTimeSeries_RejectsTooManyBuckets(t *testing.T) {
	h := NewHandler(nil, nil, allowAuthorizer{})
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

	// hour granularity over 5 years → ~43800 buckets >> the 1000 cap.
	_, err := h.GetTenantTimeSeries(ctxWithAdmin(t), uuid.New(), start, start.AddDate(5, 0, 0), "hour")
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Errorf("wide hour window: code = %v, want InvalidArgument", connect.CodeOf(err))
	}

	// 7 days at hour granularity = 168 buckets < cap → passes to the nil-pool
	// short-circuit.
	_, err = h.GetTenantTimeSeries(ctxWithAdmin(t), uuid.New(), start, start.AddDate(0, 0, 7), "hour")
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Errorf("narrow hour window: code = %v, want Unavailable (cap must not false-trip)", connect.CodeOf(err))
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

// granularityStep is four lines and it decides two separate things: how the
// SQL buckets the series, and — through maxTimeSeriesBuckets — whether a
// query is refused for being too wide. Only the "hour" branch was exercised,
// so day and week could have returned any duration at all and the existing
// test would still pass.
//
// The consequence of a wrong step is not a crash. Too small a value refuses
// legitimate queries; too large a one lets a caller ask for a million buckets
// of money data. Both look like the feature working.

func TestGranularityStep(t *testing.T) {
	for _, tc := range []struct {
		granularity string
		want        time.Duration
	}{
		{"hour", time.Hour},
		{"day", 24 * time.Hour},
		{"week", 7 * 24 * time.Hour},
		// Zero is the sentinel for "not a granularity we bucket by", and the
		// caller reads it as "skip the cap check" — so it must never be
		// returned for a supported value, and must be returned for anything
		// else. date_trunc would happily accept "minute" or "year"; exposing
		// those is a product call nobody has made.
		{"minute", 0},
		{"year", 0},
		{"", 0},
	} {
		t.Run(tc.granularity, func(t *testing.T) {
			if got := granularityStep(tc.granularity); got != tc.want {
				t.Errorf("granularityStep(%q) = %v, want %v", tc.granularity, got, tc.want)
			}
		})
	}
}

func TestGetTenantTimeSeries_BucketCapAppliesPerGranularity(t *testing.T) {
	// The cap is a bucket COUNT, so the window that trips it differs by
	// granularity. A step that is too coarse would let a wide window through;
	// one too fine would refuse a reasonable one. Both sides, per unit.
	h := NewHandler(nil, nil, allowAuthorizer{})
	start := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

	for _, tc := range []struct {
		granularity string
		underCap    time.Duration // ~900 buckets
		overCap     time.Duration // ~1100 buckets
	}{
		{"hour", 900 * time.Hour, 1100 * time.Hour},
		{"day", 900 * 24 * time.Hour, 1100 * 24 * time.Hour},
		{"week", 900 * 7 * 24 * time.Hour, 1100 * 7 * 24 * time.Hour},
	} {
		t.Run(tc.granularity+"/under", func(t *testing.T) {
			// Unavailable is the nil-pool short-circuit: the request passed
			// the cap and went on to look for a database.
			_, err := h.GetTenantTimeSeries(ctxWithAdmin(t), uuid.New(),
				start, start.Add(tc.underCap), tc.granularity)
			if connect.CodeOf(err) != connect.CodeUnavailable {
				t.Errorf("code = %v, want Unavailable — the cap false-tripped on a legitimate window",
					connect.CodeOf(err))
			}
		})
		t.Run(tc.granularity+"/over", func(t *testing.T) {
			_, err := h.GetTenantTimeSeries(ctxWithAdmin(t), uuid.New(),
				start, start.Add(tc.overCap), tc.granularity)
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Errorf("code = %v, want InvalidArgument — an over-wide window was accepted",
					connect.CodeOf(err))
			}
		})
	}
}
