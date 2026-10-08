package middleware

import (
	"context"
	"net/http"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary/unarytest"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
)

// The point of this interceptor is that a handler writing through
// logger.FromContext gets correlation without asking for it. So the assertions
// are on the fields a log line ends up carrying, not on what the interceptor
// stored — the latter would pass even if FromContext ignored all of it.

func spanCtx(ctx context.Context) (context.Context, trace.TraceID, trace.SpanID) {
	traceID, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	spanID, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	return trace.ContextWithSpanContext(ctx, trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled,
	})), traceID, spanID
}

// fieldsFromHandlerLog runs the interceptor and returns the fields a line
// written inside the handler carries.
func fieldsFromHandlerLog(t *testing.T, ctx context.Context, header http.Header) map[string]any {
	t.Helper()
	core, logs := observer.New(zapcore.InfoLevel)
	base := zap.New(core)

	probe := &unarytest.Probe{OnCall: func(ctx context.Context) error {
		logger.FromContext(ctx).Info("handler line")
		return nil
	}}

	var pairs []string
	for k, vs := range header {
		for _, v := range vs {
			pairs = append(pairs, k, v)
		}
	}

	if _, err := unarytest.CallProbe(ctx, probe, []connect.ServerInterceptor{LogContext(base)}, pairs...); err != nil {
		t.Fatalf("interceptor: %v", err)
	}
	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("got %d log entries, want 1", len(entries))
	}
	return entries[0].ContextMap()
}

// TestHandlerLineCarriesAllFourIdentifiers is the whole design in one
// assertion: a handler that says nothing about correlation gets a line that
// can be joined to its trace, its request and its tenant.
func TestHandlerLineCarriesAllFourIdentifiers(t *testing.T) {
	ctx, traceID, spanID := spanCtx(context.Background())
	tenantID := uuid.New()
	ctx = auth.WithPrincipal(ctx, &auth.Principal{Subject: "u1", TenantID: tenantID})

	h := http.Header{}
	h.Set(HeaderRequestID, "req-abc")

	got := fieldsFromHandlerLog(t, ctx, h)

	for field, want := range map[string]string{
		"trace_id":   traceID.String(),
		"span_id":    spanID.String(),
		"request_id": "req-abc",
		"tenant_id":  tenantID.String(),
	} {
		if got[field] != want {
			t.Errorf("%s = %v, want %s", field, got[field], want)
		}
	}
}

// The IAM plane runs Login and RefreshToken permissively, so many of its lines
// have no principal. Those still need to be findable — an anonymous failed
// login is exactly the line worth finding — so the absence of a tenant must
// not cost the trace.
func TestAnonymousRequestStillCorrelates(t *testing.T) {
	ctx, traceID, _ := spanCtx(context.Background())
	h := http.Header{}
	h.Set(HeaderRequestID, "req-anon")

	got := fieldsFromHandlerLog(t, ctx, h)

	if got["trace_id"] != traceID.String() {
		t.Errorf("trace_id = %v, want %s", got["trace_id"], traceID)
	}
	if got["request_id"] != "req-anon" {
		t.Errorf("request_id = %v, want req-anon", got["request_id"])
	}
	if _, ok := got["tenant_id"]; ok {
		t.Errorf("tenant_id present without a principal: %v", got["tenant_id"])
	}
}

// The tenant must come from the verified principal, never from a header. A
// caller-supplied tenant id in a log line is an assertion by the caller, which
// is precisely what a line used as an audit trail must not contain.
func TestTenantIsNotTakenFromAHeader(t *testing.T) {
	ctx, _, _ := spanCtx(context.Background())
	h := http.Header{}
	h.Set(HeaderRequestID, "req-spoof")
	h.Set("X-Tenant-Id", "00000000-0000-0000-0000-00000000dead")

	got := fieldsFromHandlerLog(t, ctx, h)

	if v, ok := got["tenant_id"]; ok {
		t.Errorf("tenant_id came from a header: %v", v)
	}
}

// Without a span the line is still written and still carries what is known. A
// zeroed trace_id would be worse than none: it looks real and finds nothing.
func TestNoSpanLeavesNoZeroedTraceID(t *testing.T) {
	h := http.Header{}
	h.Set(HeaderRequestID, "req-nospan")

	got := fieldsFromHandlerLog(t, context.Background(), h)

	if _, ok := got["trace_id"]; ok {
		t.Errorf("trace_id present without a span: %v", got["trace_id"])
	}
	if got["request_id"] != "req-nospan" {
		t.Errorf("request_id = %v, want req-nospan", got["request_id"])
	}
}

// The rpc field names the procedure, so a line can be grouped by method
// without parsing the message.
func TestLineNamesItsProcedure(t *testing.T) {
	ctx, _, _ := spanCtx(context.Background())
	got := fieldsFromHandlerLog(t, ctx, http.Header{})
	if _, ok := got["rpc"]; !ok {
		t.Error("rpc field missing from the request logger")
	}
}
