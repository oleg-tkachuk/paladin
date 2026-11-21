package objecth

import (
	"context"
	"testing"
	"uuid"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"
)

// ADR-0014 Phase 1: object-level event resource_name goes canonical (A). The
// dispatch sites resolve the collection's (backend, bucket) prefix once before
// the tx and append the user key; a resolve miss degrades to the C-shape name
// so a transient blip never blocks the event.

func TestCanonicalObjectPrefix(t *testing.T) {
	tid := uuid.MustParse("11111111-1111-1111-1111-111111111111")

	t.Run("resolves canonical prefix from binding", func(t *testing.T) {
		h := &Handler{repo: &fakeObjectRepo{meta: BucketMeta{BackendID: "primary", BucketName: "paladin"}}}
		got := h.canonicalObjectPrefix(context.Background(), opDelete, tid, "invoices")
		want := "storageBackends/primary/buckets/paladin/tenants/" + tid.String() + "/collections/invoices"
		if got != want {
			t.Fatalf("prefix = %q, want %q", got, want)
		}
	})

	t.Run("empty on lookup error, and says so", func(t *testing.T) {
		core, logs := observer.New(zapcore.DebugLevel)
		h := &Handler{repo: &fakeObjectRepo{err: errBucketStore}}
		h.SetLogger(zap.New(core))
		if got := h.canonicalObjectPrefix(context.Background(), opDelete, tid, "invoices"); got != "" {
			t.Fatalf("prefix = %q, want empty on error", got)
		}
		warnedOnce(t, logs, opDelete, tid, "invoices")
	})

	t.Run("empty when binding incomplete", func(t *testing.T) {
		// A row missing backend or bucket must not build a half-canonical name.
		core, logs := observer.New(zapcore.DebugLevel)
		h := &Handler{repo: &fakeObjectRepo{meta: BucketMeta{BackendID: "primary"}}}
		h.SetLogger(zap.New(core))
		if got := h.canonicalObjectPrefix(context.Background(), opDelete, tid, "invoices"); got != "" {
			t.Fatalf("prefix = %q, want empty when bucket missing", got)
		}
		if n := logs.Len(); n != 0 {
			t.Errorf("logged %d lines for a binding that is not an error", n)
		}
	})
}

func TestObjectResourceNameFrom(t *testing.T) {
	tid := uuid.MustParse("22222222-2222-2222-2222-222222222222")
	prefix := "storageBackends/primary/buckets/paladin/tenants/" + tid.String() + "/collections/invoices"

	t.Run("canonical when prefix present, multi-segment key preserved", func(t *testing.T) {
		got := objectResourceNameFrom(prefix, tid, "invoices", "2024/q1/report.pdf")
		want := prefix + "/objects-by-key/2024/q1/report.pdf"
		if got != want {
			t.Fatalf("name = %q, want %q", got, want)
		}
	})

	t.Run("falls back to C-shape when prefix empty", func(t *testing.T) {
		got := objectResourceNameFrom("", tid, "invoices", "report.pdf")
		want := "tenants/" + tid.String() + "/collections/invoices/objects-by-key/report.pdf"
		if got != want {
			t.Fatalf("fallback = %q, want %q", got, want)
		}
		// And it matches the standalone C-shape builder exactly.
		if got != objectResourceName(tid, "invoices", "report.pdf") {
			t.Fatalf("fallback diverged from objectResourceName")
		}
	})
}
