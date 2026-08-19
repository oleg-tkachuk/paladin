package audith

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/oleg-tkachuk/paladin-private/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin-private/internal/auth"
	"github.com/oleg-tkachuk/paladin-private/internal/policy/cedar"
)

type allowAuthorizer struct{}

func (allowAuthorizer) IsAuthorized(_ context.Context, _ *cedar.Principal, _ string, _ *cedar.Resource, _ cedar.RequestContext) (cedar.Decision, error) {
	return cedar.DecisionAllow, nil
}

type fakeAuditRepo struct {
	pages [][]admindomain.AuditEntry // each call to List returns next page
	calls int
}

func (f *fakeAuditRepo) Insert(context.Context, admindomain.AuditEntry) error { return nil }
func (f *fakeAuditRepo) InsertWithOutbox(ctx context.Context, e admindomain.AuditEntry, onInserted func(context.Context, pgx.Tx) error) error {
	if err := f.Insert(ctx, e); err != nil {
		return err
	}
	if onInserted != nil {
		return onInserted(ctx, nil)
	}
	return nil
}
func (f *fakeAuditRepo) Get(context.Context, uuid.UUID) (admindomain.AuditEntry, error) {
	return admindomain.AuditEntry{}, admindomain.ErrNotFound
}
func (f *fakeAuditRepo) List(_ context.Context, _ admindomain.ListAuditArgs) ([]admindomain.AuditEntry, string, error) {
	if f.calls >= len(f.pages) {
		return nil, "", nil
	}
	page := f.pages[f.calls]
	f.calls++
	next := ""
	if f.calls < len(f.pages) && len(page) > 0 {
		last := page[len(page)-1]
		next = last.At.Format(time.RFC3339Nano) + "/" + last.EntryID.String()
	}
	return page, next, nil
}

func ctxWithPlatformAdmin(t *testing.T) context.Context {
	t.Helper()
	return auth.WithPrincipal(context.Background(), &auth.Principal{
		TenantID: uuid.New(),
		Subject:  "ops@example.com",
		Roles:    []string{"platform.admin"},
		Audience: "paladin-admin",
	})
}

func mkEntry(at time.Time) admindomain.AuditEntry {
	return admindomain.AuditEntry{
		EntryID:       uuid.Must(uuid.NewV7()),
		At:            at,
		ActorSubject:  "alice",
		ActorAudience: "paladin-admin",
		Action:        "/paladin.admin.v1.BucketService/UpdateBucket",
		ResourceName:  "storageBackends/b1/buckets/b",
		BeforeJSON:    []byte(`{"display_name":"old"}`),
		AfterJSON:     []byte(`{"display_name":"new"}`),
	}
}

func TestExportAuditLogPaginatesAndProjects(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	repo := &fakeAuditRepo{
		pages: [][]admindomain.AuditEntry{
			{mkEntry(t0), mkEntry(t0.Add(time.Second))},
			{mkEntry(t0.Add(2 * time.Second))},
		},
	}
	h := NewHandler(repo, allowAuthorizer{})
	res, err := h.ExportAuditLog(ctxWithPlatformAdmin(t), "", "")
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if res.RowCount != 3 {
		t.Errorf("row_count: got %d want 3", res.RowCount)
	}
	if res.Truncated {
		t.Error("should not be truncated under cap")
	}
	if len(res.Entries) != 3 {
		t.Fatalf("entries len: got %d want 3", len(res.Entries))
	}
	// Before/After must round-trip as raw JSON, not double-encoded.
	if string(res.Entries[0].Before) != `{"display_name":"old"}` {
		t.Errorf("before raw: got %q", string(res.Entries[0].Before))
	}
	if string(res.Entries[0].After) != `{"display_name":"new"}` {
		t.Errorf("after raw: got %q", string(res.Entries[0].After))
	}
}

func TestExportAuditLogTruncatesAtCap(t *testing.T) {
	// Stuff more than exportRowCap entries into one page so the loop hits
	// the cap mid-page and returns Truncated=true.
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	bulk := make([]admindomain.AuditEntry, exportRowCap+5)
	for i := range bulk {
		bulk[i] = mkEntry(t0.Add(time.Duration(i) * time.Millisecond))
	}
	repo := &fakeAuditRepo{pages: [][]admindomain.AuditEntry{bulk}}
	h := NewHandler(repo, allowAuthorizer{})
	res, err := h.ExportAuditLog(ctxWithPlatformAdmin(t), "", "")
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if !res.Truncated {
		t.Error("expected Truncated=true at cap")
	}
	if res.RowCount != exportRowCap {
		t.Errorf("row_count: got %d want %d", res.RowCount, exportRowCap)
	}
}

func TestListAuditLogAppliesCELFilter(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	good := mkEntry(t0)
	bad := mkEntry(t0.Add(time.Second))
	bad.ErrorMessage = "boom"
	repo := &fakeAuditRepo{pages: [][]admindomain.AuditEntry{{good, bad}}}
	h := NewHandler(repo, allowAuthorizer{})

	// is_error == true filters out the success row.
	got, _, err := h.ListAuditLog(ctxWithPlatformAdmin(t),
		admindomain.ListAuditArgs{PageSize: 50}, "is_error == true")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(got) != 1 || got[0].EntryID != bad.EntryID {
		t.Errorf("got %d entries, want 1 (the failed one)", len(got))
	}
}

func TestListAuditLogRejectsBadFilter(t *testing.T) {
	repo := &fakeAuditRepo{}
	h := NewHandler(repo, allowAuthorizer{})
	_, _, err := h.ListAuditLog(ctxWithPlatformAdmin(t),
		admindomain.ListAuditArgs{PageSize: 50}, "no_such_field == 1")
	if err == nil {
		t.Fatal("expected error for unknown CEL identifier")
	}
}

func TestExportAuditLogAppliesCELFilter(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	a := mkEntry(t0)
	a.Action = "/paladin.admin.v1.BucketService/CreateBucket"
	b := mkEntry(t0.Add(time.Second))
	b.Action = "/paladin.admin.v1.BucketService/DeleteBucket"
	repo := &fakeAuditRepo{pages: [][]admindomain.AuditEntry{{a, b}}}
	h := NewHandler(repo, allowAuthorizer{})

	res, err := h.ExportAuditLog(ctxWithPlatformAdmin(t),
		`action.endsWith("DeleteBucket")`, "")
	if err != nil {
		t.Fatalf("export: %v", err)
	}
	if res.RowCount != 1 {
		t.Errorf("row_count: got %d want 1", res.RowCount)
	}
	if len(res.Entries) != 1 || res.Entries[0].Action != b.Action {
		t.Errorf("expected only DeleteBucket entry; got %+v", res.Entries)
	}
}

func TestDecodeCursorRoundTrip(t *testing.T) {
	at := time.Date(2026, 5, 5, 12, 0, 0, 0, time.UTC)
	id := uuid.New()
	tok := at.Format(time.RFC3339Nano) + "/" + id.String()
	gotAt, gotID := decodeCursor(tok)
	if !gotAt.Equal(at) {
		t.Errorf("at: got %v want %v", gotAt, at)
	}
	if gotID != id {
		t.Errorf("id: got %v want %v", gotID, id)
	}
}

func TestDecodeCursorInvalid(t *testing.T) {
	at, id := decodeCursor("garbage")
	if !at.IsZero() || id != uuid.Nil {
		t.Errorf("expected zero-values for malformed cursor")
	}
}
