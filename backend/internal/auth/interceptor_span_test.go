package auth

import (
	"context"
	"testing"

	"github.com/google/uuid"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

// annotateSpan is the ADR-0001 follow-up that stamps the caller's tenant onto
// the active RPC span. These tests pin the contract: tenant_id/tenant_slug are
// set when present, and the helper is a safe no-op for an anonymous/super-admin
// principal so it can run on every RPC.
func recordingTracerCtx(t *testing.T) (context.Context, func() []sdktrace.ReadOnlySpan) {
	t.Helper()
	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	ctx, span := tp.Tracer("test").Start(context.Background(), "rpc")
	return ctx, func() []sdktrace.ReadOnlySpan {
		span.End()
		return rec.Ended()
	}
}

func attrValue(span sdktrace.ReadOnlySpan, key string) (string, bool) {
	for _, kv := range span.Attributes() {
		if string(kv.Key) == key {
			return kv.Value.AsString(), true
		}
	}
	return "", false
}

func TestAnnotateSpan_SetsTenant(t *testing.T) {
	ctx, ended := recordingTracerCtx(t)
	tid := uuid.New()
	annotateSpan(ctx, &Principal{TenantID: tid, TenantSlug: "acme"})

	spans := ended()
	if len(spans) != 1 {
		t.Fatalf("expected 1 span, got %d", len(spans))
	}
	if got, ok := attrValue(spans[0], "paladin.tenant_id"); !ok || got != tid.String() {
		t.Errorf("paladin.tenant_id: got %q present=%v, want %q", got, ok, tid)
	}
	if got, ok := attrValue(spans[0], "paladin.tenant_slug"); !ok || got != "acme" {
		t.Errorf("paladin.tenant_slug: got %q present=%v, want acme", got, ok)
	}
}

func TestAnnotateSpan_NoTenantIsNoop(t *testing.T) {
	ctx, ended := recordingTracerCtx(t)
	// nil principal and zero-tenant principal must both be safe no-ops.
	annotateSpan(ctx, nil)
	annotateSpan(ctx, &Principal{TenantID: uuid.Nil, Subject: "super-admin"})

	spans := ended()
	if _, ok := attrValue(spans[0], "paladin.tenant_id"); ok {
		t.Error("paladin.tenant_id should not be set for a zero-tenant principal")
	}
}
