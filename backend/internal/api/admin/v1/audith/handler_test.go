package audith

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
)

type allowAuthorizer struct{}

func (allowAuthorizer) IsAuthorized(_ context.Context, _ *cedar.Principal, _ string, _ *cedar.Resource, _ cedar.RequestContext) (cedar.Decision, error) {
	return cedar.DecisionAllow, nil
}

type fakeAuditRepo struct {
	pages [][]admindomain.AuditEntry // each call to List returns next page
	calls int
	args  admindomain.ListAuditArgs   // the last List's arguments
	seen  []admindomain.ListAuditArgs // every List's arguments, in order
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
func (f *fakeAuditRepo) List(_ context.Context, args admindomain.ListAuditArgs) ([]admindomain.AuditEntry, string, error) {
	f.args = args
	f.seen = append(f.seen, args)
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

// A tenant's trail reaches the query as a trail — not as the actor tenant,
// which would drop a platform admin's work inside it — and only a platform
// admin may name another tenant's.
func TestListAuditLogTrailTenant(t *testing.T) {
	own := uuid.New()
	other := uuid.New()
	member := auth.WithPrincipal(context.Background(), &auth.Principal{
		TenantID: own, Subject: "alice", Roles: []string{"tenant.admin"}, Audience: "paladin-admin",
	})
	cases := []struct {
		name       string
		ctx        context.Context
		trail      uuid.UUID
		wantDenied bool
		wantActor  uuid.UUID
	}{
		{name: "platform admin, another tenant", ctx: ctxWithPlatformAdmin(t), trail: other, wantActor: uuid.Nil},
		{name: "member, own tenant", ctx: member, trail: own, wantActor: own},
		{name: "member, another tenant", ctx: member, trail: other, wantDenied: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			repo := &fakeAuditRepo{}
			h := NewHandler(repo, allowAuthorizer{})
			_, _, err := h.ListAuditLog(tc.ctx, admindomain.ListAuditArgs{PageSize: 50, TrailTenantID: tc.trail}, "")
			if tc.wantDenied {
				if connect.CodeOf(err) != connect.CodePermissionDenied {
					t.Fatalf("err = %v, want PermissionDenied", err)
				}
				if repo.calls != 0 {
					t.Error("the log was read for a denied caller")
				}
				return
			}
			if err != nil {
				t.Fatalf("list: %v", err)
			}
			if repo.args.TrailTenantID != tc.trail {
				t.Errorf("trail = %s, want %s", repo.args.TrailTenantID, tc.trail)
			}
			if repo.args.ActorTenantID != tc.wantActor {
				t.Errorf("actor tenant = %s, want %s", repo.args.ActorTenantID, tc.wantActor)
			}
		})
	}
}

// A filter the store cannot apply — a disjunction — keeps whatever matches
// among the newest rows of the whole log. The handler reads on until the page
// is full, so a match that is not among them is still found, and the page is
// not handed back empty with a cursor.
func TestListAuditLogFillsAPageAcrossBatches(t *testing.T) {
	t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	entry := func(i int, subject string) admindomain.AuditEntry {
		e := mkEntry(t0.Add(-time.Duration(i) * time.Second)) // newest first
		e.ActorSubject = subject
		return e
	}
	const filter = `actor_subject == "bob" || actor_subject == "carol"`

	t.Run("matches spread over batches", func(t *testing.T) {
		d, e := entry(3, "bob"), entry(4, "carol")
		repo := &fakeAuditRepo{pages: [][]admindomain.AuditEntry{
			{entry(0, "alice"), entry(1, "alice")},
			{entry(2, "alice"), d},
			{e, entry(5, "alice")},
		}}
		got, next, err := NewHandler(repo, allowAuthorizer{}).ListAuditLog(ctxWithPlatformAdmin(t),
			admindomain.ListAuditArgs{PageSize: 2}, filter)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(got) != 2 || got[0].EntryID != d.EntryID || got[1].EntryID != e.EntryID {
			t.Fatalf("got %v, want the two matches", subjects(got))
		}
		// Full in the middle of the third batch: the next page starts after
		// the last row returned, not after the batch, whose last row was
		// never looked at by the caller.
		if want := admindomain.AuditCursor(e); next != want {
			t.Errorf("cursor = %q, want %q", next, want)
		}
		// Each batch after the first resumes where the one before ended.
		if len(repo.seen) != 3 || repo.seen[1].AfterID != repo.pages[0][1].EntryID ||
			repo.seen[2].AfterID != repo.pages[1][1].EntryID {
			t.Errorf("batches resumed at %v", afterIDs(repo.seen))
		}
	})

	t.Run("the log ends first", func(t *testing.T) {
		d := entry(1, "bob")
		repo := &fakeAuditRepo{pages: [][]admindomain.AuditEntry{
			{entry(0, "alice"), d},
			{entry(2, "alice")},
		}}
		got, next, err := NewHandler(repo, allowAuthorizer{}).ListAuditLog(ctxWithPlatformAdmin(t),
			admindomain.ListAuditArgs{PageSize: 2}, filter)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(got) != 1 || got[0].EntryID != d.EntryID || next != "" {
			t.Errorf("got %v cursor %q, want the one match and no cursor", subjects(got), next)
		}
	})

	t.Run("the last row of the log fills the page", func(t *testing.T) {
		d, e := entry(0, "bob"), entry(1, "carol")
		repo := &fakeAuditRepo{pages: [][]admindomain.AuditEntry{{d, e}}}
		_, next, err := NewHandler(repo, allowAuthorizer{}).ListAuditLog(ctxWithPlatformAdmin(t),
			admindomain.ListAuditArgs{PageSize: 2}, filter)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if next != "" {
			t.Errorf("cursor = %q past the end of the log, want none", next)
		}
	})

	t.Run("the scan bound is spent", func(t *testing.T) {
		var pages [][]admindomain.AuditEntry
		for b := 0; b < maxFilteredBatches+1; b++ {
			pages = append(pages, []admindomain.AuditEntry{entry(2*b, "alice"), entry(2*b+1, "alice")})
		}
		repo := &fakeAuditRepo{pages: pages}
		got, next, err := NewHandler(repo, allowAuthorizer{}).ListAuditLog(ctxWithPlatformAdmin(t),
			admindomain.ListAuditArgs{PageSize: 2}, filter)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if repo.calls != maxFilteredBatches {
			t.Errorf("read %d batches, want the bound of %d", repo.calls, maxFilteredBatches)
		}
		// Nothing found yet, but the log goes on: the caller can continue.
		if len(got) != 0 || next == "" {
			t.Errorf("got %v cursor %q, want an empty page with a cursor", subjects(got), next)
		}
	})
}

func subjects(es []admindomain.AuditEntry) []string {
	out := make([]string, 0, len(es))
	for _, e := range es {
		out = append(out, e.ActorSubject)
	}
	return out
}

func afterIDs(as []admindomain.ListAuditArgs) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(as))
	for _, a := range as {
		out = append(out, a.AfterID)
	}
	return out
}
