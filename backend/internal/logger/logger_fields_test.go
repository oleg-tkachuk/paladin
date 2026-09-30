package logger

import (
	"context"
	"testing"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/reqctx"
)

// The existing suite could only check that enrichment does not panic. An
// observer core makes the emitted fields assertable, which is what actually
// matters: a dropped trace_id or tenant_id silently breaks log correlation.

// observed returns a logger writing into an in-memory core plus its log sink.
func observed() (*zap.Logger, *observer.ObservedLogs) {
	core, logs := observer.New(zapcore.DebugLevel)
	return zap.New(core), logs
}

// fields flattens the single emitted entry's fields into a map.
func fields(t *testing.T, logs *observer.ObservedLogs) map[string]any {
	t.Helper()
	all := logs.All()
	if len(all) != 1 {
		t.Fatalf("want exactly 1 log entry, got %d", len(all))
	}
	return all[0].ContextMap()
}

// ─── parsers ───────────────────────────────────────────────────────────────

func TestParseLevel(t *testing.T) {
	cases := map[string]zapcore.Level{
		"debug":   zapcore.DebugLevel,
		"DEBUG":   zapcore.DebugLevel, // case-insensitive
		"info":    zapcore.InfoLevel,
		"warn":    zapcore.WarnLevel,
		"warning": zapcore.WarnLevel, // both spellings accepted
		"error":   zapcore.ErrorLevel,
		"dpanic":  zapcore.DPanicLevel,
		"panic":   zapcore.PanicLevel,
		"fatal":   zapcore.FatalLevel,
	}
	for in, want := range cases {
		got, err := parseLevel(in)
		if err != nil {
			t.Errorf("parseLevel(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseLevel(%q) = %v, want %v", in, got, want)
		}
	}
}

// An unknown level must fail loudly at boot rather than silently defaulting —
// a typo'd level that quietly became "info" could hide debug output or, worse,
// suppress errors.
func TestParseLevelRejectsUnknown(t *testing.T) {
	for _, s := range []string{"", "verbose", "trace", "nonsense"} {
		if _, err := parseLevel(s); err == nil {
			t.Errorf("parseLevel(%q): want an error", s)
		}
	}
}

func TestParseEncoding(t *testing.T) {
	for in, want := range map[string]string{
		"json":    "json",
		"":        "json", // unset defaults to structured output
		"JSON":    "json",
		"console": "console",
		"CONSOLE": "console",
	} {
		got, err := parseEncoding(in)
		if err != nil {
			t.Errorf("parseEncoding(%q): %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("parseEncoding(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseEncodingRejectsUnknown(t *testing.T) {
	for _, s := range []string{"logfmt", "xml", "text"} {
		if _, err := parseEncoding(s); err == nil {
			t.Errorf("parseEncoding(%q): want an error", s)
		}
	}
}

// The encoder keys are the log schema every downstream parser keys off, so a
// rename here breaks ingestion.
func TestEncoderConfig(t *testing.T) {
	c := encoderConfig()
	for name, got := range map[string]string{
		"TimeKey":       c.TimeKey,
		"LevelKey":      c.LevelKey,
		"NameKey":       c.NameKey,
		"MessageKey":    c.MessageKey,
		"StacktraceKey": c.StacktraceKey,
	} {
		if got == "" {
			t.Errorf("%s must be set", name)
		}
	}
	if c.TimeKey != "time" || c.LevelKey != "level" || c.MessageKey != "msg" {
		t.Errorf("log schema drifted: time=%q level=%q msg=%q", c.TimeKey, c.LevelKey, c.MessageKey)
	}
}

// ─── New ───────────────────────────────────────────────────────────────────

func TestNew(t *testing.T) {
	l, err := New(config.Logger{
		Level:  "info",
		Format: "json",
		Fields: config.LoggerFields{Service: "paladin", Env: "test"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if l == nil {
		t.Fatal("New returned nil")
	}
}

func TestNewWithSampling(t *testing.T) {
	l, err := New(config.Logger{
		Level: "debug", Format: "console",
		Development: true, DisableCaller: true, DisableStacktrace: true,
		Sampling: config.LogSampling{Enabled: true, Initial: 10, Thereafter: 100},
	})
	if err != nil || l == nil {
		t.Fatalf("New with sampling: %v", err)
	}
}

// A bad level or format must surface at construction, not at first log call.
func TestNewRejectsBadConfig(t *testing.T) {
	if _, err := New(config.Logger{Level: "nonsense", Format: "json"}); err == nil {
		t.Error("a bad level must fail the build")
	}
	if _, err := New(config.Logger{Level: "info", Format: "nonsense"}); err == nil {
		t.Error("a bad format must fail the build")
	}
}

// ─── staticFields ──────────────────────────────────────────────────────────

func TestStaticFields(t *testing.T) {
	t.Setenv(config.DefaultK8sNamespaceEnvKey, "")

	got := staticFields(config.Logger{
		Fields: config.LoggerFields{Service: "paladin-core", Env: "prod"},
	})
	if len(got) != 2 {
		t.Fatalf("want service+env only outside k8s, got %d fields", len(got))
	}
}

// In-cluster the namespace must be attached automatically so logs from two
// namespaces running the same service stay distinguishable.
func TestStaticFieldsAddsNamespaceInCluster(t *testing.T) {
	t.Setenv(config.DefaultK8sNamespaceEnvKey, "paladin")

	got := staticFields(config.Logger{})
	if len(got) != 3 {
		t.Fatalf("want the namespace field in-cluster, got %d fields", len(got))
	}

	l, logs := observed()
	l.With(got...).Info("x")
	if ns := fields(t, logs)["namespace"]; ns != "paladin" {
		t.Errorf("namespace = %v, want paladin", ns)
	}
}

// ─── context plumbing ──────────────────────────────────────────────────────

func TestWithContextRoundTrip(t *testing.T) {
	l, logs := observed()
	ctx := WithContext(context.Background(), l)

	FromContext(ctx).Info("hello")

	if len(logs.All()) != 1 {
		t.Fatal("FromContext must return the stashed logger")
	}
}

// With no logger in the context the accessor must still return a usable
// logger rather than nil.
func TestFromContextFallsBackToGlobal(t *testing.T) {
	if FromContext(context.Background()) == nil {
		t.Fatal("FromContext must never return nil")
	}
}

func TestWithNameAndNamed(t *testing.T) {
	l, logs := observed()
	ctx := WithContext(context.Background(), l)

	Named(ctx, "sub").Info("a")
	if got := logs.All()[0].LoggerName; got != "sub" {
		t.Errorf("Named logger name = %q, want sub", got)
	}

	FromContext(WithName(ctx, "child")).Info("b")
	if got := logs.All()[1].LoggerName; got != "child" {
		t.Errorf("WithName logger name = %q, want child", got)
	}
}

func TestActorContext(t *testing.T) {
	if got := ActorFromContext(context.Background()); got != "" {
		t.Errorf("unset actor = %q, want empty", got)
	}
	ctx := WithActor(context.Background(), "alice")
	if got := ActorFromContext(ctx); got != "alice" {
		t.Errorf("actor = %q, want alice", got)
	}
	// A non-string value under the key must not panic the accessor.
	bad := context.WithValue(context.Background(), actorCtxKey{}, 42)
	if got := ActorFromContext(bad); got != "" {
		t.Errorf("non-string actor = %q, want empty", got)
	}
}

// ─── enrich ────────────────────────────────────────────────────────────────

func TestEnrichAddsRequestAndTenant(t *testing.T) {
	l, logs := observed()
	ctx := reqctx.WithTenantID(context.Background(), "tenant-7")
	ctx = reqctx.WithRequestID(ctx, "req-9")

	FromContext(WithContext(ctx, l)).Info("x")

	f := fields(t, logs)
	if f["tenant_id"] != "tenant-7" {
		t.Errorf("tenant_id = %v", f["tenant_id"])
	}
	if f["request_id"] != "req-9" {
		t.Errorf("request_id = %v", f["request_id"])
	}
}

// Absent context values must not emit empty keys — a blank tenant_id on every
// line is worse than no key at all for log filtering.
func TestEnrichOmitsAbsentValues(t *testing.T) {
	l, logs := observed()

	FromContext(WithContext(context.Background(), l)).Info("x")

	f := fields(t, logs)
	for _, k := range []string{"tenant_id", "request_id", "trace_id", "span_id"} {
		if _, ok := f[k]; ok {
			t.Errorf("%s must be omitted when unset, got %v", k, f[k])
		}
	}
}

// Trace correlation is the whole reason enrich consults the span context.
func TestEnrichAddsTraceIDs(t *testing.T) {
	traceID, err := trace.TraceIDFromHex("0102030405060708090a0b0c0d0e0f10")
	if err != nil {
		t.Fatalf("trace id: %v", err)
	}
	spanID, err := trace.SpanIDFromHex("0102030405060708")
	if err != nil {
		t.Fatalf("span id: %v", err)
	}
	sc := trace.NewSpanContext(trace.SpanContextConfig{
		TraceID: traceID, SpanID: spanID, TraceFlags: trace.FlagsSampled,
	})
	l, logs := observed()
	ctx := trace.ContextWithSpanContext(context.Background(), sc)

	FromContext(WithContext(ctx, l)).Info("x")

	f := fields(t, logs)
	if f["trace_id"] != traceID.String() {
		t.Errorf("trace_id = %v, want %s", f["trace_id"], traceID)
	}
	if f["span_id"] != spanID.String() {
		t.Errorf("span_id = %v, want %s", f["span_id"], spanID)
	}
}

// An invalid span context must be skipped rather than emitting all-zero ids.
func TestEnrichIgnoresInvalidSpanContext(t *testing.T) {
	l, logs := observed()
	ctx := trace.ContextWithSpanContext(context.Background(), trace.NewSpanContext(trace.SpanContextConfig{}))

	FromContext(WithContext(ctx, l)).Info("x")

	if _, ok := fields(t, logs)["trace_id"]; ok {
		t.Error("an invalid span context must not emit a trace_id")
	}
}

// ─── audit channel ─────────────────────────────────────────────────────────

// The app/audit split is what keeps audit records out of the noisy app stream;
// the log_type tag is how downstream routing tells them apart.
func TestReplaceGlobalsSplitsAppAndAudit(t *testing.T) {
	base, logs := observed()
	ReplaceGlobals(base)
	t.Cleanup(func() { globalAppLogger, globalAuditLogger = nil, nil })

	zap.L().Info("app line")
	AuditFromContext(context.Background()).Info("audit line")

	all := logs.All()
	if len(all) != 2 {
		t.Fatalf("want 2 entries, got %d", len(all))
	}
	if got := all[0].ContextMap()["log_type"]; got != "app" {
		t.Errorf("app log_type = %v", got)
	}
	if got := all[1].ContextMap()["log_type"]; got != "audit" {
		t.Errorf("audit log_type = %v", got)
	}
}

func TestAuditFromContextAttachesActorAndEnrichment(t *testing.T) {
	base, logs := observed()
	ReplaceGlobals(base)
	t.Cleanup(func() { globalAppLogger, globalAuditLogger = nil, nil })

	ctx := WithActor(reqctx.WithTenantID(context.Background(), "t-1"), "alice")
	AuditFromContext(ctx).Info("did a thing")

	f := fields(t, logs)
	if f["actor"] != "alice" {
		t.Errorf("actor = %v, want alice", f["actor"])
	}
	if f["tenant_id"] != "t-1" {
		t.Errorf("audit records must carry the same enrichment, got %v", f["tenant_id"])
	}
}

// Only the audit channel carries the actor — app logs must stay free of it.
func TestActorIsAuditOnly(t *testing.T) {
	l, logs := observed()
	ctx := WithActor(context.Background(), "alice")

	FromContext(WithContext(ctx, l)).Info("app line")

	if _, ok := fields(t, logs)["actor"]; ok {
		t.Error("the app channel must not carry the actor")
	}
}

// Before ReplaceGlobals runs, the audit accessor must still work rather than
// dereferencing a nil global.
func TestAuditFromContextBeforeReplaceGlobals(t *testing.T) {
	saved := globalAuditLogger
	globalAuditLogger = nil
	t.Cleanup(func() { globalAuditLogger = saved })

	if AuditFromContext(context.Background()) == nil {
		t.Fatal("audit logger must never be nil")
	}
}
