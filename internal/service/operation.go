package service

import (
	"context"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/domain"
	"github.com/oleg-tkachuk/paladin/internal/metrics"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
)

// opCtx encapsulates span lifecycle, metrics recording, and status tracking
// for a service operation. Use beginOp to create and defer op.end() to finalize.
type opCtx struct {
	span   trace.Span
	status string
	start  time.Time
	metric string
	ctx    context.Context
}

// beginOp starts a traced, metered service operation.
func beginOp(ctx context.Context, opName, metricName string, attrs ...attribute.KeyValue) (context.Context, *opCtx) {
	ctx, span := otel.Tracer(TracerName).Start(ctx, opName)
	span.SetAttributes(attrs...)

	return ctx, &opCtx{
		span:   span,
		start:  time.Now(),
		metric: metricName,
		ctx:    ctx,
	}
}

// end finalizes the span and records the operation metric.
func (o *opCtx) end() {
	o.span.End()
	if o.metric != "" {
		metrics.RecordObjectOp(o.ctx, o.metric, o.status, o.start)
	}
}

// fail records an error on the span and sets status to error.
func (o *opCtx) fail(err error) {
	o.span.RecordError(err)
	o.span.SetStatus(codes.Error, err.Error())
	o.status = domain.StatusError
}

// failStatus records an error with a custom status (e.g., "conflict").
func (o *opCtx) failStatus(err error, status string) {
	o.span.RecordError(err)
	o.span.SetStatus(codes.Error, err.Error())
	o.status = status
}

// succeed marks the operation as successful.
func (o *opCtx) succeed() {
	o.status = domain.StatusSuccess
	o.span.SetStatus(codes.Ok, "")
}

// succeedMsg marks the operation as successful with a message.
func (o *opCtx) succeedMsg(msg string) {
	o.status = domain.StatusSuccess
	o.span.SetStatus(codes.Ok, msg)
}

// addAttrs adds additional span attributes mid-operation.
func (o *opCtx) addAttrs(attrs ...attribute.KeyValue) {
	o.span.SetAttributes(attrs...)
}
