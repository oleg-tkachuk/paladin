package eventingest

import (
	"context"
	"strings"
	"testing"
	"uuid"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/storage/storagepath"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// fakeLookup records the (collection, key) the handler resolves to and returns
// ErrNoRows from LookupObjectByKey so Handle skips before the state machine.
type fakeLookup struct {
	gotCollection string
	gotKey        string
	lookupCalled  bool
	binding       sqlc.GetCollectionRow
	bindingErr    error
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

func uploadedEvent(tenant uuid.UUID, collection, key string) CloudEvent {
	return CloudEvent{
		ID:   "evt-1",
		Type: EventTypeUploaded,
		SubjectFields: SubjectFields{
			TenantID:   tenant.String(),
			Collection: collection,
			Key:        key,
		},
	}
}

// The collection is one segment of the bucket path, so the source's split is
// the object's: a nested collection and its parent no longer compete for the
// same path, and the handler looks up exactly what the source parsed.
func TestHandle_LooksUpTheSourceSplit(t *testing.T) {
	for _, tc := range [][2]string{
		{"invoices/2026/q1", "report.pdf"},
		{"invoices", "2026/q1/report.pdf"},
		{"assets", "logo.png"},
		// Stored before the boundary bounded a key's length and text: the
		// object it names still owns it.
		{"assets", strings.Repeat("k", storagepath.MaxSegmentBytes+1)},
		{"assets", "a\nb"},
	} {
		f := &fakeLookup{}
		h := &PromoteHandler{Lookup: f, Logger: zap.NewNop()}
		if err := h.Handle(context.Background(), uploadedEvent(uuid.New(), tc[0], tc[1])); err != nil {
			t.Fatalf("Handle(%q, %q): %v", tc[0], tc[1], err)
		}
		if f.gotCollection != tc[0] || f.gotKey != tc[1] {
			t.Errorf("looked up (%q, %q), want (%q, %q)", f.gotCollection, f.gotKey, tc[0], tc[1])
		}
	}
}

// A key Paladin would refuse was written by someone else: no object owns it,
// so the event is skipped without a lookup.
func TestHandle_SkipsAKeyOutsideTheContract(t *testing.T) {
	for _, key := range []string{"a//b", "a/./b", "..", "dir/"} {
		f := &fakeLookup{}
		h := &PromoteHandler{Lookup: f, Logger: zap.NewNop()}
		if err := h.Handle(context.Background(), uploadedEvent(uuid.New(), "docs", key)); err != nil {
			t.Fatalf("Handle(%q): %v", key, err)
		}
		if f.lookupCalled {
			t.Errorf("key %q was looked up", key)
		}
	}
}
