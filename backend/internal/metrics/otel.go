package metrics

import (
	"context"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

var (
	meter = otel.Meter("github.com/oleg-tkachuk/paladin")

	// resourceNameShapeTotal feeds the Phase-3 decision on deprecating a
	// redundant object-name shape (see internal/api/connectshim/resolve).
	// Flows over the OTLP pipeline like every other PALADIN metric.
	resourceNameShapeTotal metric.Int64Counter
)

func init() {
	var err error
	resourceNameShapeTotal, err = meter.Int64Counter(
		"paladin_resource_name_shape_total",
		metric.WithDescription("ObjectKey resource-name shapes received at the connectshim edge, by shape"),
	)
	if err != nil {
		otel.Handle(err)
	}
}

// RecordResourceNameShape counts which object-name shape (canonical / tenant /
// bare) a request used at the connectshim edge.
func RecordResourceNameShape(ctx context.Context, shape string) {
	resourceNameShapeTotal.Add(ctx, 1, metric.WithAttributes(attribute.String("shape", shape)))
}
