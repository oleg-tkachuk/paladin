// Package logger wires zap into the Paladin request pipeline.
//
// # Log message style guide
//
// All messages are written for grep-ability and consistency. The conventions
// below are enforced by code review — there's no automatic linter for them.
//
//   - Sentence-case off: messages are lowercase except for proper nouns
//     (HTTP, SQS, S3, Paladin, …) and acronyms.
//   - No trailing punctuation. The level + structured fields carry the
//     information; periods and ellipses add noise.
//   - Failures use the "failed to $verb" idiom and always attach the cause
//     via zap.Error(err). Don't repeat the error string in the message.
//   - State changes / successes use noun-phrase or past-tense verb form:
//     "config loaded", "shutdown signal received", "starting".
//   - Don't prefix messages with the component name — every worker / store
//     gets a Named() child logger that already prepends the component.
package logger

import (
	"context"
	"fmt"
	"os"
	"strings"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/reqctx"
)

// New builds the application logger from config. Sampling, structured
// fields and the k8s namespace (when running in-cluster) are attached
// automatically.
func New(c config.Logger) (*zap.Logger, error) {
	lvl, err := parseLevel(c.Level)
	if err != nil {
		return nil, err
	}
	enc, err := parseEncoding(c.Format)
	if err != nil {
		return nil, err
	}

	cfg := zap.Config{
		Level:             zap.NewAtomicLevelAt(lvl),
		Development:       c.Development,
		DisableCaller:     c.DisableCaller,
		DisableStacktrace: c.DisableStacktrace,
		Encoding:          enc,
		OutputPaths:       []string{"stdout"},
		ErrorOutputPaths:  []string{"stderr"},
		EncoderConfig:     encoderConfig(),
	}
	if c.Sampling.Enabled {
		cfg.Sampling = &zap.SamplingConfig{
			Initial:    c.Sampling.Initial,
			Thereafter: c.Sampling.Thereafter,
		}
	}

	log, err := cfg.Build()
	if err != nil {
		return nil, fmt.Errorf("build zap logger: %w", err)
	}
	return log.With(staticFields(c)...), nil
}

// NewBootstrapLogger returns a logger usable before config is loaded.
// Returns an error so the caller can surface it during early startup
// instead of silently swallowing a misconfigured zap build.
func NewBootstrapLogger() (*zap.Logger, error) {
	cfg := zap.NewProductionConfig()
	cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	l, err := cfg.Build()
	if err != nil {
		return nil, fmt.Errorf("build bootstrap logger: %w", err)
	}
	return l, nil
}

// ─── parsers ────────────────────────────────────────────────────────────────

func parseLevel(s string) (zapcore.Level, error) {
	switch strings.ToLower(s) {
	case "debug":
		return zapcore.DebugLevel, nil
	case "info":
		return zapcore.InfoLevel, nil
	case "warn", "warning":
		return zapcore.WarnLevel, nil
	case "error":
		return zapcore.ErrorLevel, nil
	case "dpanic":
		return zapcore.DPanicLevel, nil
	case "panic":
		return zapcore.PanicLevel, nil
	case "fatal":
		return zapcore.FatalLevel, nil
	default:
		return 0, fmt.Errorf("unsupported log level: %s", s)
	}
}

func parseEncoding(s string) (string, error) {
	switch strings.ToLower(s) {
	case "json", "":
		return "json", nil
	case "console":
		return "console", nil
	default:
		return "", fmt.Errorf("unsupported log format: %s", s)
	}
}

func encoderConfig() zapcore.EncoderConfig {
	return zapcore.EncoderConfig{
		TimeKey:        "time",
		LevelKey:       "level",
		NameKey:        "logger",
		MessageKey:     "msg",
		StacktraceKey:  "stacktrace",
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		EncodeDuration: zapcore.StringDurationEncoder,
		LineEnding:     zapcore.DefaultLineEnding,
	}
}

func staticFields(c config.Logger) []zap.Field {
	out := []zap.Field{
		zap.String("service", c.Fields.Service),
		zap.String("env", c.Fields.Env),
	}
	if v := os.Getenv(config.DefaultK8sNamespaceEnvKey); v != "" {
		out = append(out, zap.String("namespace", v))
	}
	return out
}

// ─── globals (app + audit segregation) ─────────────────────────────────────

var (
	globalAppLogger   *zap.Logger
	globalAuditLogger *zap.Logger
)

// ReplaceGlobals splits the supplied logger into "app" and "audit" children
// (distinguished by `log_type`) and installs the app child as zap's global
// logger. AuditFromContext pulls from the audit child.
func ReplaceGlobals(log *zap.Logger) {
	globalAppLogger = log.With(zap.String("log_type", "app"))
	globalAuditLogger = log.With(zap.String("log_type", "audit"))
	zap.ReplaceGlobals(globalAppLogger)
}

// ─── context helpers ───────────────────────────────────────────────────────

type ctxKey struct{}
type actorCtxKey struct{}

// WithContext returns a child context carrying the given logger.
func WithContext(ctx context.Context, l *zap.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, l)
}

// WithName returns a child context with the context logger renamed —
// shorthand for WithContext(ctx, FromContext(ctx).Named(name)).
func WithName(ctx context.Context, name string) context.Context {
	return WithContext(ctx, FromContext(ctx).Named(name))
}

// Named returns a child of the context logger with the given name appended.
func Named(ctx context.Context, name string) *zap.Logger {
	return FromContext(ctx).Named(name)
}

// WithActor attaches an actor identity (subject) to the context. Only
// AuditFromContext consults it — app logs don't need actor noise.
func WithActor(ctx context.Context, actor string) context.Context {
	return context.WithValue(ctx, actorCtxKey{}, actor)
}

// ActorFromContext returns the actor stashed by WithActor, or "" if unset.
func ActorFromContext(ctx context.Context) string {
	if a, ok := ctx.Value(actorCtxKey{}).(string); ok {
		return a
	}
	return ""
}

// FromContext returns the context logger (or zap.L() as a fallback) enriched
// with trace, request and tenant identifiers. Cheap to call repeatedly —
// each enrichment is a `zap.With` which copies fields lazily.
func FromContext(ctx context.Context) *zap.Logger {
	l, _ := ctx.Value(ctxKey{}).(*zap.Logger)
	if l == nil {
		l = zap.L()
	}
	return enrich(ctx, l)
}

// AuditFromContext returns the audit-channel logger (log_type=audit)
// enriched with trace, request, tenant and actor. Falls back to
// zap.L().With(log_type=audit) when ReplaceGlobals hasn't run yet.
func AuditFromContext(ctx context.Context) *zap.Logger {
	l := globalAuditLogger
	if l == nil {
		l = zap.L().With(zap.String("log_type", "audit"))
	}
	l = enrich(ctx, l)
	if a := ActorFromContext(ctx); a != "" {
		l = l.With(zap.String("actor", a))
	}
	return l
}

// enrich tacks trace_id / span_id / request_id / tenant_id on the logger
// when the corresponding context values are present. Shared by the app and
// audit accessors so they never drift apart.
func enrich(ctx context.Context, l *zap.Logger) *zap.Logger {
	if span := trace.SpanFromContext(ctx); span.SpanContext().IsValid() {
		sc := span.SpanContext()
		l = l.With(
			zap.String("trace_id", sc.TraceID().String()),
			zap.String("span_id", sc.SpanID().String()),
		)
	}
	if rid := reqctx.RequestID(ctx, ""); rid != "" {
		l = l.With(zap.String("request_id", rid))
	}
	if tid := reqctx.TenantID(ctx, ""); tid != "" {
		l = l.With(zap.String("tenant_id", tid))
	}
	return l
}
