package observability

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/config"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetricgrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	otelprom "go.opentelemetry.io/otel/exporters/prometheus"
	"go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"

	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc/credentials/insecure"
)

type ShutdownFunc func(context.Context) error

// MetricsHandler is the /metrics HTTP handler, non-nil only when
// otel.metrics_exporter is "prometheus". The caller mounts it; this package
// has no mux of its own and should not grow one.
type MetricsHandler http.Handler

// InitOTel wires tracing and metrics from config.
//
// Metrics have two possible destinations and they are not interchangeable:
// an OTLP collector accepts a push, a Prometheus scraper performs a pull, and
// a cluster generally runs one or the other. Paladin shipped only the push
// path into a cluster whose collector only pulls — so every instrument in the
// tree exported to nothing, which is invisible from the code and obvious the
// moment you look for a series that is not there.
//
// Traces are always OTLP: there is no pull model for a span.
func InitOTel(ctx context.Context, cfg config.OTel) (ShutdownFunc, MetricsHandler, error) {
	if !cfg.Enabled {
		return func(context.Context) error { return nil }, nil, nil
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(cfg.Resource.ServiceName),
			attribute.String("deployment.environment", cfg.Resource.DeploymentEnvironment),
		),
	)
	if err != nil {
		return nil, nil, err
	}

	// Trace Exporter
	var traceExporter sdktrace.SpanExporter
	if cfg.Protocol == "http" {
		traceExporter, err = otlptracehttp.New(ctx,
			otlptracehttp.WithEndpoint(cfg.Endpoint),
			otlptracehttp.WithInsecure(),
		)
	} else {
		traceExporter, err = otlptracegrpc.New(ctx,
			otlptracegrpc.WithEndpoint(cfg.Endpoint),
			otlptracegrpc.WithTLSCredentials(insecure.NewCredentials()),
		)
	}
	if err != nil {
		return nil, nil, err
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(traceExporter,
			sdktrace.WithBatchTimeout(2*time.Second),
		),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)

	// Metric reader — pull or push, per config.
	var (
		reader         metric.Reader
		metricsHandler MetricsHandler
	)
	switch cfg.MetricsExporter {
	case "none":
		// Traces only. A no-op MeterProvider stays installed, so every
		// instrument in the tree still records into nothing safely.

	case "prometheus":
		promExporter, perr := otelprom.New()
		if perr != nil {
			return nil, nil, perr
		}
		reader = promExporter
		metricsHandler = promhttp.Handler()

	default: // "otlp" — the historical behaviour, kept as the default.
		var metricExporter metric.Exporter
		if cfg.Protocol == "http" {
			metricExporter, err = otlpmetrichttp.New(ctx,
				otlpmetrichttp.WithEndpoint(cfg.Endpoint),
				otlpmetrichttp.WithInsecure(),
			)
		} else {
			metricExporter, err = otlpmetricgrpc.New(ctx,
				otlpmetricgrpc.WithEndpoint(cfg.Endpoint),
				otlpmetricgrpc.WithTLSCredentials(insecure.NewCredentials()),
			)
		}
		if err != nil {
			return nil, nil, err
		}
		reader = metric.NewPeriodicReader(metricExporter, metric.WithInterval(3*time.Second))
	}

	var mp *metric.MeterProvider
	if reader != nil {
		mp = metric.NewMeterProvider(
			metric.WithReader(reader),
			metric.WithResource(res),
		)
		otel.SetMeterProvider(mp)
	}

	shutdown := func(ctx context.Context) error {
		var errs error
		if err := tp.Shutdown(ctx); err != nil {
			errs = errors.Join(errs, err)
		}
		if mp != nil {
			if err := mp.Shutdown(ctx); err != nil {
				errs = errors.Join(errs, err)
			}
		}

		return errs
	}

	return shutdown, metricsHandler, nil
}
