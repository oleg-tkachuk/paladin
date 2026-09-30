package eventingest

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// fakeLookup records the (collection, key) the handler resolves to and returns
// ErrNoRows from LookupObjectByKey so Handle skips before the state machine —
// isolating the multi-segment disambiguation glue.
type fakeLookup struct {
	resolveReturn string
	resolveErr    error
	gotCollection string
	gotKey        string
	lookupCalled  bool
	binding       sqlc.GetCollectionRow
	bindingErr    error
}

func (f *fakeLookup) ResolveCollectionPrefix(_ context.Context, _ pgtype.UUID, _ string) (string, error) {
	return f.resolveReturn, f.resolveErr
}

func (f *fakeLookup) GetCollection(_ context.Context, _ pgtype.UUID, _ string) (sqlc.GetCollectionRow, error) {
	return f.binding, f.bindingErr
}

func (f *fakeLookup) LookupObjectByKey(_ context.Context, _ pgtype.UUID, collection, key string) (sqlc.LookupObjectByKeyRow, error) {
	f.lookupCalled = true
	f.gotCollection = collection
	f.gotKey = key
	return sqlc.LookupObjectByKeyRow{}, pgx.ErrNoRows
}

func uploadedEvent(tenant uuid.UUID, naiveOK, naiveKey string) CloudEvent {
	return CloudEvent{
		ID:   "evt-1",
		Type: EventTypeUploaded,
		SubjectFields: SubjectFields{
			TenantID:   tenant.String(),
			Collection: naiveOK,
			Key:        naiveKey,
		},
	}
}

// TestHandle_MultiSegmentCollectionDisambiguation: the source naively split the
// tail at the first segment (OK=`invoices`, key=`2026/q1/report.pdf`), but the
// real OK is the longer `invoices/2026/q1`. The handler must recombine the
// tail, take the longest-prefix OK, and look the object up with the corrected
// split.
func TestHandle_MultiSegmentCollectionDisambiguation(t *testing.T) {
	tenant := uuid.New()
	f := &fakeLookup{resolveReturn: "invoices/2026/q1"}
	h := &PromoteHandler{Lookup: f, Logger: zap.NewNop()}

	if err := h.Handle(context.Background(), uploadedEvent(tenant, "invoices", "2026/q1/report.pdf")); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !f.lookupCalled {
		t.Fatal("LookupObjectByKey was not called")
	}
	if f.gotCollection != "invoices/2026/q1" {
		t.Errorf("resolved collection = %q, want invoices/2026/q1", f.gotCollection)
	}
	if f.gotKey != "report.pdf" {
		t.Errorf("resolved key = %q, want report.pdf", f.gotKey)
	}
}

// TestHandle_SingleSegmentUnchanged: when the prefix-match returns the same OK
// the source already had (the common single-segment case), the split is
// unchanged.
func TestHandle_SingleSegmentUnchanged(t *testing.T) {
	tenant := uuid.New()
	f := &fakeLookup{resolveReturn: "assets"}
	h := &PromoteHandler{Lookup: f, Logger: zap.NewNop()}

	if err := h.Handle(context.Background(), uploadedEvent(tenant, "assets", "logo.png")); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if f.gotCollection != "assets" || f.gotKey != "logo.png" {
		t.Errorf("split = (%q, %q), want (assets, logo.png)", f.gotCollection, f.gotKey)
	}
}

// TestHandle_NoRegisteredPrefixKeepsSourceSplit: ErrNoRows from the prefix
// resolver (no OK matches) → keep the source's split; the object lookup then
// skips it as unknown (no error from Handle).
func TestHandle_NoRegisteredPrefixKeepsSourceSplit(t *testing.T) {
	tenant := uuid.New()
	f := &fakeLookup{resolveErr: pgx.ErrNoRows}
	h := &PromoteHandler{Lookup: f, Logger: zap.NewNop()}

	if err := h.Handle(context.Background(), uploadedEvent(tenant, "ghost", "x/y.bin")); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if f.gotCollection != "ghost" || f.gotKey != "x/y.bin" {
		t.Errorf("split = (%q, %q), want the source's (ghost, x/y.bin)", f.gotCollection, f.gotKey)
	}
}
