package eventingest

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// ADR-0010 Phase 1: ingest-side object events must carry the SAME canonical
// (A-shape) resource_name the data-plane object handler emits, so an
// implicit-mode (storage-event) upload and an explicit CompleteObject upload
// are indistinguishable to a subscriber filtering on resource_name.

func bindingRow(backend, bucket string) sqlc.GetCollectionRow {
	return sqlc.GetCollectionRow{Collection: sqlc.Collection{BackendID: backend, BucketName: bucket}}
}

func TestIngestObjectResourceName(t *testing.T) {
	tid := uuid.MustParse("33333333-3333-3333-3333-333333333333")

	t.Run("canonical when the binding resolves", func(t *testing.T) {
		h := &PromoteHandler{Lookup: &fakeLookup{binding: bindingRow("primary", "paladin")}, Logger: zap.NewNop()}
		got := h.objectResourceName(context.Background(), tid.String(), "invoices", "2026/q1.pdf")
		want := "storageBackends/primary/buckets/paladin/tenants/" + tid.String() + "/collections/invoices/objects-by-key/2026/q1.pdf"
		if got != want {
			t.Fatalf("resource_name = %q, want %q", got, want)
		}
	})

	t.Run("C-shape fallback on lookup error", func(t *testing.T) {
		h := &PromoteHandler{Lookup: &fakeLookup{bindingErr: errors.New("db down")}, Logger: zap.NewNop()}
		got := h.objectResourceName(context.Background(), tid.String(), "invoices", "k")
		want := "tenants/" + tid.String() + "/collections/invoices/objects-by-key/k"
		if got != want {
			t.Fatalf("resource_name = %q, want C-shape %q", got, want)
		}
	})

	t.Run("C-shape fallback when the binding is incomplete", func(t *testing.T) {
		h := &PromoteHandler{Lookup: &fakeLookup{binding: bindingRow("primary", "")}, Logger: zap.NewNop()}
		got := h.objectResourceName(context.Background(), tid.String(), "invoices", "k")
		if got != "tenants/"+tid.String()+"/collections/invoices/objects-by-key/k" {
			t.Fatalf("resource_name = %q, want C-shape fallback", got)
		}
	})

	t.Run("C-shape fallback on unparseable tenant id", func(t *testing.T) {
		h := &PromoteHandler{Lookup: &fakeLookup{binding: bindingRow("primary", "paladin")}, Logger: zap.NewNop()}
		got := h.objectResourceName(context.Background(), "not-a-uuid", "invoices", "k")
		if got != "tenants/not-a-uuid/collections/invoices/objects-by-key/k" {
			t.Fatalf("resource_name = %q, want C-shape fallback", got)
		}
	})
}
