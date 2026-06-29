package metrics

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var (
	meter = otel.Meter("github.com/oleg-tkachuk/paladin")

	// Service Metrics
	objectOpDuration metric.Float64Histogram
	objectOpTotal    metric.Int64Counter

	// S3 Metrics
	s3OpDuration metric.Float64Histogram
	s3OpTotal    metric.Int64Counter

	// DB Metrics
	dbQueryDuration metric.Float64Histogram
	dbQueryTotal    metric.Int64Counter

	// Cache Metrics
	cacheOpTotal metric.Int64Counter

	// Resource-name shape (connectshim edge) — feeds the Phase 3 decision
	// on deprecating a redundant object-name shape.
	resourceNameShapeTotal metric.Int64Counter
)

func init() {
	var err error

	// Service Metrics
	objectOpDuration, err = meter.Float64Histogram(
		"paladin_object_operation_duration_seconds",
		metric.WithDescription("Duration of object operations"),
		metric.WithUnit("s"),
	)
	if err != nil {
		otel.Handle(err)
	}

	objectOpTotal, err = meter.Int64Counter(
		"paladin_object_operations_total",
		metric.WithDescription("Total number of object operations"),
	)
	if err != nil {
		otel.Handle(err)
	}

	// S3 Metrics
	s3OpDuration, err = meter.Float64Histogram(
		"paladin_s3_operation_duration_seconds",
		metric.WithDescription("Duration of S3 operations"),
		metric.WithUnit("s"),
	)
	if err != nil {
		otel.Handle(err)
	}

	s3OpTotal, err = meter.Int64Counter(
		"paladin_s3_operations_total",
		metric.WithDescription("Total number of S3 operations"),
	)
	if err != nil {
		otel.Handle(err)
	}

	// DB Metrics
	dbQueryDuration, err = meter.Float64Histogram(
		"paladin_db_query_duration_seconds",
		metric.WithDescription("Duration of database queries"),
		metric.WithUnit("s"),
	)
	if err != nil {
		otel.Handle(err)
	}

	dbQueryTotal, err = meter.Int64Counter(
		"paladin_db_queries_total",
		metric.WithDescription("Total number of database queries"),
	)
	if err != nil {
		otel.Handle(err)
	}

	// Cache Metrics
	cacheOpTotal, err = meter.Int64Counter(
		"paladin_cache_operations_total",
		metric.WithDescription("Total number of cache operations"),
	)
	if err != nil {
		otel.Handle(err)
	}

	resourceNameShapeTotal, err = meter.Int64Counter(
		"paladin_resource_name_shape_total",
		metric.WithDescription("ObjectKey resource-name shapes received at the connectshim edge, by shape"),
	)
	if err != nil {
		otel.Handle(err)
	}
}

// OTel helpers

func RecordObjectOp(ctx context.Context, operation, status string, start time.Time) {
	elapsed := time.Since(start).Seconds()
	attrs := metric.WithAttributes(
		attribute.String("operation", operation),
		attribute.String("status", status),
	)
	objectOpDuration.Record(ctx, elapsed, attrs)
	objectOpTotal.Add(ctx, 1, attrs)
}

func RecordS3Op(ctx context.Context, operation, status string, start time.Time) {
	elapsed := time.Since(start).Seconds()
	attrs := metric.WithAttributes(
		attribute.String("operation", operation),
		attribute.String("status", status),
	)
	s3OpDuration.Record(ctx, elapsed, attrs)
	s3OpTotal.Add(ctx, 1, attrs)
}

func RecordDbQuery(ctx context.Context, query, status string, start time.Time) {
	elapsed := time.Since(start).Seconds()
	attrs := metric.WithAttributes(
		attribute.String("query", query),
		attribute.String("status", status),
	)
	dbQueryDuration.Record(ctx, elapsed, attrs)
	dbQueryTotal.Add(ctx, 1, attrs)
}

func RecordCacheOp(ctx context.Context, operation, result string) {
	attrs := metric.WithAttributes(
		attribute.String("operation", operation),
		attribute.String("result", result),
	)
	cacheOpTotal.Add(ctx, 1, attrs)
}

// RecordResourceNameShape counts which object-name shape (canonical / tenant /
// bare) a request used at the connectshim edge. Feeds the Phase 3 decision on
// whether any shape is unused and can be deprecated. Flows over the OTLP
// pipeline like every other PALADIN metric.
func RecordResourceNameShape(ctx context.Context, shape string) {
	resourceNameShapeTotal.Add(ctx, 1, metric.WithAttributes(attribute.String("shape", shape)))
}
