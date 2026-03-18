package logger

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/utils"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// New creates a zap.Logger with sampling and environment fields.
func New(c config.Logger) (*zap.Logger, error) {
	var lvl zapcore.Level

	switch strings.ToLower(c.Level) {
	case "debug":
		lvl = zapcore.DebugLevel
	case "info":
		lvl = zapcore.InfoLevel
	case "warn", "warning":
		lvl = zapcore.WarnLevel
	case "error":
		lvl = zapcore.ErrorLevel
	case "dpanic":
		lvl = zapcore.DPanicLevel
	case "panic":
		lvl = zapcore.PanicLevel
	case "fatal":
		lvl = zapcore.FatalLevel
	default:
		return nil, fmt.Errorf("unsupported log level: %s", c.Level)
	}

	var enc string

	switch strings.ToLower(c.Format) {
	case "json", "":
		enc = "json"
	case "console":
		enc = "console"
	default:
		return nil, fmt.Errorf("unsupported log format: %s", c.Format)
	}

	cfg := zap.Config{
		Level:             zap.NewAtomicLevelAt(lvl),
		Development:       c.Development,
		DisableCaller:     c.DisableCaller,
		DisableStacktrace: c.DisableStacktrace,
		Encoding:          enc,
		OutputPaths:       []string{"stdout"},
		ErrorOutputPaths:  []string{"stderr"},
		EncoderConfig: zapcore.EncoderConfig{
			TimeKey:        "time",
			LevelKey:       "level",
			NameKey:        "logger",
			MessageKey:     "msg",
			StacktraceKey:  "stacktrace",
			EncodeLevel:    zapcore.LowercaseLevelEncoder,
			EncodeTime:     zapcore.ISO8601TimeEncoder,
			EncodeDuration: zapcore.StringDurationEncoder,
			LineEnding:     zapcore.DefaultLineEnding,
		},
		Sampling: &zap.SamplingConfig{
			Initial:    c.Sampling.Initial,
			Thereafter: c.Sampling.Thereafter,
		},
	}

	// Disable sampling if not enabled
	if !c.Sampling.Enabled {
		cfg.Sampling = nil
	}

	log, err := cfg.Build()
	if err != nil {
		return nil, err
	}

	// Attach fields from config
	fields := []zap.Field{
		zap.String("service", c.Fields.Service),
		zap.String("env", c.Fields.Env),
	}

	// Optional: include additional env fields without leaking secrets
	if v := os.Getenv(config.DefaultK8sNamespaceEnvKey); v != "" {
		fields = append(fields, zap.String("namespace", v))
	}

	return log.With(fields...), nil
}

var (
	globalAppLogger   *zap.Logger
	globalAuditLogger *zap.Logger
)

// ReplaceGlobals replaces the global zap logger and sugared logger.
// It creates segregated "app" and "audit" loggers natively using zap.With.
func ReplaceGlobals(log *zap.Logger) {
	globalAppLogger = log.With(zap.String("log_type", "app"))
	globalAuditLogger = log.With(zap.String("log_type", "audit"))
	zap.ReplaceGlobals(globalAppLogger)
}

// NewBootstrapLogger returns a simple logger for early startup.
// It uses a production-ready configuration that writes to stdout.
func NewBootstrapLogger() *zap.Logger {
	cfg := zap.NewProductionConfig()
	cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	l, _ := cfg.Build()

	return l
}

type ctxKey struct{}
type actorCtxKey struct{}

// WithActor adds an actor identity to the context.
func WithActor(ctx context.Context, actor string) context.Context {
	return context.WithValue(ctx, actorCtxKey{}, actor)
}

// ActorFromContext retrieves the actor identity from the context.
func ActorFromContext(ctx context.Context) string {
	if actor, ok := ctx.Value(actorCtxKey{}).(string); ok {
		return actor
	}
	return ""
}

// WithContext returns a new context with the given logger attached.
func WithContext(ctx context.Context, l *zap.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// Named returns the logger from the context, adding the given name to it.
func Named(ctx context.Context, name string) *zap.Logger {
	return FromContext(ctx).Named(name)
}

// WithName adds a component name to the context logger and returns the new context.
func WithName(ctx context.Context, name string) context.Context {
	l := FromContext(ctx).Named(name)
	return WithContext(ctx, l)
}

// FromContext returns the logger attached to the context, or the global logger if none is found.
// It also ensures the logger has the latest trace context.
func FromContext(ctx context.Context) *zap.Logger {
	l := zap.L()
	if ctxL, ok := ctx.Value(ctxKey{}).(*zap.Logger); ok {
		l = ctxL
	}

	// Always attempt to enrich with current trace context
	if span := trace.SpanFromContext(ctx); span.SpanContext().IsValid() {
		sc := span.SpanContext()
		l = l.With(
			zap.String("trace_id", sc.TraceID().String()),
			zap.String("span_id", sc.SpanID().String()),
		)
	}

	// Enrich with request and tenant IDs
	if rid := utils.RequestIDFromContext(ctx, ""); rid != "" {
		l = l.With(zap.String("request_id", rid))
	}
	if tid := utils.TenantIDFromContext(ctx, ""); tid != "" {
		l = l.With(zap.String("tenant_id", tid))
	}

	return l
}

// AuditFromContext returns the global audit logger enriched with trace context, tenant, request, and actor.
func AuditFromContext(ctx context.Context) *zap.Logger {
	l := globalAuditLogger
	if l == nil {
		l = zap.L().With(zap.String("log_type", "audit"))
	}

	// Always attempt to enrich with current trace context
	if span := trace.SpanFromContext(ctx); span.SpanContext().IsValid() {
		sc := span.SpanContext()
		l = l.With(
			zap.String("trace_id", sc.TraceID().String()),
			zap.String("span_id", sc.SpanID().String()),
		)
	}

	// Enrich with request, tenant IDs and actor
	if rid := utils.RequestIDFromContext(ctx, ""); rid != "" {
		l = l.With(zap.String("request_id", rid))
	}
	if tid := utils.TenantIDFromContext(ctx, ""); tid != "" {
		l = l.With(zap.String("tenant_id", tid))
	}
	if actor := ActorFromContext(ctx); actor != "" {
		l = l.With(zap.String("actor", actor))
	}

	return l
}
