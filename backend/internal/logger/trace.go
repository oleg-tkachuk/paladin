package logger

import (
	"context"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

// WithTrace returns a logger carrying the active span's trace and span ids.
//
// Traces and logs are exported to the same backend and, until now, could not
// be joined: a slow trace gave no way to find the lines it produced, and an
// error line gave no way to find the request that produced it. Correlating
// them by timestamp works only when one request is in flight, which is the
// case that never needs debugging.
//
// The field names are the OTel log-correlation convention (trace_id /
// span_id), which is what Grafana's "logs to traces" link expects — so this
// wires up the jump between the two views without further configuration.
//
// Returns the logger untouched when the context carries no sampled span, so
// callers on a background worker or a test path pay nothing and log nothing
// misleading: an unsampled span's id resolves to zeroes, and a zeroed trace_id
// in a log line is worse than an absent one — it looks like a real id that
// finds nothing.
func WithTrace(ctx context.Context, log *zap.Logger) *zap.Logger {
	if log == nil {
		return log
	}
	sc := trace.SpanContextFromContext(ctx)
	if !sc.IsValid() {
		return log
	}
	return log.With(
		zap.String("trace_id", sc.TraceID().String()),
		zap.String("span_id", sc.SpanID().String()),
	)
}
