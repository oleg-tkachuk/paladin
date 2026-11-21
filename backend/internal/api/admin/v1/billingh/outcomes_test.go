package billingh

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/meter"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
)

// logged is h logging, at level and above, to the lines it returns.
func logged(h *Handler, level zapcore.Level) (*Handler, *observer.ObservedLogs) {
	core, logs := observer.New(level)
	h.SetLogger(zap.New(core))
	return h, logs
}

// noSums is a ledger with no charge in it.
func noSums(meter.SumChargesRequest) []meter.ChargeSum { return nil }

// Each billing read, as a platform admin, over the default period.
var billingReads = map[string]func(*Handler, context.Context) error{
	opGetTenantSummary: func(h *Handler, ctx context.Context) error {
		_, err := h.GetTenantSummary(ctx, uuid.New(), time.Time{}, time.Time{})
		return err
	},
	opGetTenantTimeSeries: func(h *Handler, ctx context.Context) error {
		_, err := h.GetTenantTimeSeries(ctx, uuid.New(), time.Time{}, time.Time{}, "")
		return err
	},
	opGetPlatformTimeSeries: func(h *Handler, ctx context.Context) error {
		_, err := h.GetPlatformTimeSeries(ctx, time.Time{}, time.Time{}, "")
		return err
	},
	opListTenantTimeSeries: func(h *Handler, ctx context.Context) error {
		_, err := h.ListTenantTimeSeries(ctx, []uuid.UUID{uuid.New()}, time.Time{}, time.Time{}, "")
		return err
	},
	opListTenantSpend: func(h *Handler, ctx context.Context) error {
		_, _, err := h.ListTenantSpend(ctx, time.Time{}, time.Time{}, 0)
		return err
	},
}

// A ledger that fails is logged once at Error, naming the read, whatever
// code it is answered by.
func TestLedgerFailuresAreLogged(t *testing.T) {
	for op, read := range billingReads {
		t.Run(op, func(t *testing.T) {
			h, logs := logged(NewHandler(&fakeLedger{err: limes.ErrStoreUnavailable}, nil, allowAuthorizer{}), zapcore.WarnLevel)
			if err := read(h, ctxWithAdmin(t)); connect.CodeOf(err) != connect.CodeUnavailable {
				t.Fatalf("code = %v (%v), want Unavailable", connect.CodeOf(err), err)
			}
			lines := logs.All()
			if len(lines) != 1 || lines[0].Level != zapcore.ErrorLevel || lines[0].ContextMap()["op"] != op {
				t.Fatalf("want one error line for %s, got %v", op, lines)
			}
		})
	}
}

// The ledger refusing what the request named — a tenant id it does not take,
// say — is the caller's InvalidArgument, a warning; its scope check failing
// is the server's own, internal and an error.
func TestLedgerRefusalsWarnAndFaultsError(t *testing.T) {
	for _, tc := range []struct {
		err   error
		want  connect.Code
		level zapcore.Level
	}{
		{limes.ErrInvalidRequest, connect.CodeInvalidArgument, zapcore.WarnLevel},
		{limes.ErrScopeMismatch, connect.CodeInternal, zapcore.ErrorLevel},
		{limes.ErrStoreTimeout, connect.CodeUnavailable, zapcore.ErrorLevel},
	} {
		t.Run(tc.err.Error(), func(t *testing.T) {
			h, logs := logged(NewHandler(&fakeLedger{err: tc.err}, nil, allowAuthorizer{}), zapcore.WarnLevel)
			_, err := h.GetTenantSummary(ctxWithAdmin(t), uuid.New(), time.Time{}, time.Time{})
			if connect.CodeOf(err) != tc.want {
				t.Fatalf("code = %v (%v), want %v", connect.CodeOf(err), err, tc.want)
			}
			if logs.Len() != 1 || logs.All()[0].Level != tc.level {
				t.Fatalf("want one %v line, got %v", tc.level, logs.All())
			}
		})
	}
}

func TestStoreFault(t *testing.T) {
	for code, want := range map[connect.Code]bool{
		connect.CodeInternal: true, connect.CodeUnknown: true, connect.CodeUnavailable: true,
		connect.CodeDataLoss: true, connect.CodeDeadlineExceeded: true,
		connect.CodeInvalidArgument: false, connect.CodePermissionDenied: false,
		connect.CodeFailedPrecondition: false, connect.CodeResourceExhausted: false,
	} {
		if got := storeFault(code); got != want {
			t.Errorf("storeFault(%v) = %v, want %v", code, got, want)
		}
	}
}

// A refused read is logged once at Warn: a policy that denies it, and a
// deployment with no ledger.
func TestBillingRefusalsWarn(t *testing.T) {
	for op, read := range billingReads {
		for name, h := range map[string]*Handler{
			"denied":       NewHandler(&fakeLedger{sums: noSums}, nil, denyAuthorizer{}),
			"no subsystem": NewHandler(nil, nil, allowAuthorizer{}),
		} {
			t.Run(op+": "+name, func(t *testing.T) {
				h, logs := logged(h, zapcore.WarnLevel)
				if err := read(h, ctxWithAdmin(t)); err == nil {
					t.Fatal("refused nothing")
				}
				lines := logs.All()
				if len(lines) != 1 || lines[0].Level != zapcore.WarnLevel || lines[0].ContextMap()["op"] != op {
					t.Fatalf("want one warning for %s, got %v", op, lines)
				}
			})
		}
	}
}

// A request the handler refuses as malformed is a warning too.
func TestMalformedBillingRequestsWarn(t *testing.T) {
	future := time.Now().Add(time.Hour)
	for name, read := range map[string]func(*Handler, context.Context) error{
		"summary period backwards": func(h *Handler, ctx context.Context) error {
			_, err := h.GetTenantSummary(ctx, uuid.New(), future, time.Now())
			return err
		},
		"series granularity": func(h *Handler, ctx context.Context) error {
			_, err := h.GetTenantTimeSeries(ctx, uuid.New(), time.Time{}, time.Time{}, "month")
			return err
		},
		"platform granularity": func(h *Handler, ctx context.Context) error {
			_, err := h.GetPlatformTimeSeries(ctx, time.Time{}, time.Time{}, "month")
			return err
		},
		"tenants granularity": func(h *Handler, ctx context.Context) error {
			_, err := h.ListTenantTimeSeries(ctx, nil, time.Time{}, time.Time{}, "month")
			return err
		},
		"spend limit": func(h *Handler, ctx context.Context) error {
			_, _, err := h.ListTenantSpend(ctx, time.Time{}, time.Time{}, MaxSpendLimit+1)
			return err
		},
		"spend period backwards": func(h *Handler, ctx context.Context) error {
			_, _, err := h.ListTenantSpend(ctx, future, time.Now(), 0)
			return err
		},
	} {
		t.Run(name, func(t *testing.T) {
			h, logs := logged(NewHandler(&fakeLedger{sums: noSums}, nil, allowAuthorizer{}), zapcore.WarnLevel)
			if err := read(h, ctxWithAdmin(t)); connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("code = %v (%v), want InvalidArgument", connect.CodeOf(err), err)
			}
			if logs.FilterLevelExact(zapcore.WarnLevel).Len() != 1 || logs.Len() != 1 {
				t.Fatalf("want one warning, got %v", logs.All())
			}
		})
	}
}

// A budget that cannot be read fails the summary: a summary without its
// ceiling reads as a tenant with no cap, which is not what the store said.
// A tenant with no budget has none to show.
func TestSummaryOfAnUnreadableBudgetFails(t *testing.T) {
	for _, tc := range []struct {
		name    string
		err     error
		fails   bool
		want    connect.Code
		ceiling bool
	}{
		{name: "read", ceiling: true},
		{name: "no budget", err: meter.ErrTenantBudgetNotFound},
		{name: "store down", err: limes.ErrStoreUnavailable, fails: true, want: connect.CodeUnavailable},
		{name: "store failed", err: errors.New("connection reset"), fails: true, want: connect.CodeInternal},
	} {
		t.Run(tc.name, func(t *testing.T) {
			usage := budgetReader{budget: meter.TenantBudget{Ceiling: limes.BudgetCeiling{UnitCode: usd}}, err: tc.err}
			h, logs := logged(NewHandler(&fakeLedger{sums: noSums}, usage, allowAuthorizer{}), zapcore.ErrorLevel)
			sum, err := h.GetTenantSummary(ctxWithAdmin(t), uuid.New(), time.Time{}, time.Time{})
			if (err != nil) != tc.fails {
				t.Fatalf("err = %v, want failed = %v", err, tc.fails)
			}
			if tc.fails {
				if connect.CodeOf(err) != tc.want || !errors.Is(err, tc.err) || logs.Len() != 1 {
					t.Fatalf("err = %v (%v), %d error lines; want %v from the store's, logged once", err, connect.CodeOf(err), logs.Len(), tc.want)
				}
				return
			}
			if (sum.Ceiling != nil) != tc.ceiling {
				t.Errorf("ceiling = %v, want shown = %v", sum.Ceiling, tc.ceiling)
			}
		})
	}
}

// A time series only takes its unit from the budget, so one it cannot read
// still answers, in the platform's currency, and logs why at Error.
func TestTimeSeriesOfAnUnreadableBudgetFallsBackAndLogs(t *testing.T) {
	usage := budgetReader{err: errors.New("connection reset")}
	h, logs := logged(NewHandler(&fakeLedger{sums: noSums}, usage, allowAuthorizer{}).WithCurrency(eur), zapcore.ErrorLevel)
	ts, err := h.GetTenantTimeSeries(ctxWithAdmin(t), uuid.New(), time.Time{}, time.Time{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if ts.UnitCode != eur || logs.Len() != 1 {
		t.Fatalf("unit = %q, %d error lines; want %s and one line", ts.UnitCode, logs.Len(), eur)
	}
}

// The budget's unit stands in for a series with no charge; with no budget,
// the platform's currency does, and nothing is logged.
func TestTimeSeriesUnitFallback(t *testing.T) {
	for name, tc := range map[string]struct {
		usage meter.UsageStore
		want  string
	}{
		"budget":    {budgetReader{budget: meter.TenantBudget{Ceiling: limes.BudgetCeiling{UnitCode: usd}}}, usd},
		"no budget": {budgetReader{err: meter.ErrTenantBudgetNotFound}, eur},
		"no store":  {nil, eur},
	} {
		t.Run(name, func(t *testing.T) {
			h, logs := logged(NewHandler(&fakeLedger{sums: noSums}, tc.usage, allowAuthorizer{}).WithCurrency(eur), zapcore.WarnLevel)
			ts, err := h.GetTenantTimeSeries(ctxWithAdmin(t), uuid.New(), time.Time{}, time.Time{}, "")
			if err != nil || ts.UnitCode != tc.want || logs.Len() != 0 {
				t.Fatalf("unit = %q, err = %v, logs = %v; want %s, quietly", ts.UnitCode, err, logs.All(), tc.want)
			}
		})
	}
}

// Currency refused is a warning.
func TestCurrencyRefusalWarns(t *testing.T) {
	h, logs := logged(NewHandler(nil, nil, denyAuthorizer{}), zapcore.WarnLevel)
	if _, err := h.Currency(ctxWithAdmin(t)); err == nil {
		t.Fatal("refused nothing")
	}
	if logs.Len() != 1 {
		t.Fatalf("want one warning, got %v", logs.All())
	}
}

var _ cedar.Authorizer = allowAuthorizer{}
