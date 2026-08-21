package logger

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

func TestWithTraceAttachesIDsFromASampledSpan(t *testing.T) {
	core, logs := observer.New(zapcore.InfoLevel)
	base := zap.New(core)

	traceID, _ := trace.TraceIDFromHex("4bf92f3577b34da6a3ce929d0e0e4736")
	spanID, _ := trace.SpanIDFromHex("00f067aa0ba902b7")
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled,
	}))

	WithTrace(ctx, base).Info("hello")

	entries := logs.All()
	if len(entries) != 1 {
		t.Fatalf("got %d entries, want 1", len(entries))
	}
	fields := entries[0].ContextMap()
	if fields["trace_id"] != traceID.String() {
		t.Errorf("trace_id = %v, want %s", fields["trace_id"], traceID)
	}
	if fields["span_id"] != spanID.String() {
		t.Errorf("span_id = %v, want %s", fields["span_id"], spanID)
	}
}

// A zeroed trace_id is worse than an absent one: it looks like a real id and
// finds nothing when pasted into the trace view.
func TestWithTraceAddsNothingWithoutASpan(t *testing.T) {
	core, logs := observer.New(zapcore.InfoLevel)
	WithTrace(context.Background(), zap.New(core)).Info("hello")

	fields := logs.All()[0].ContextMap()
	if _, ok := fields["trace_id"]; ok {
		t.Errorf("trace_id present without a span: %v", fields["trace_id"])
	}
	if _, ok := fields["span_id"]; ok {
		t.Errorf("span_id present without a span: %v", fields["span_id"])
	}
}

func TestWithTraceToleratesANilLogger(t *testing.T) {
	if got := WithTrace(context.Background(), nil); got != nil {
		t.Errorf("got %v, want nil", got)
	}
}
