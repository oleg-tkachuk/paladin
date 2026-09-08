package eventingest

// Every early exit in PromoteHandler.Handle returns nil, and that is the whole
// risk surface: `return nil` tells the worker the event is settled, so the
// dedup row stands and the broker acks. An event skipped that should have been
// retried is gone — no error, no retry, no trace beyond a log line. An event
// retried that should have been skipped comes back forever.
//
// The disambiguation switch is tested next door. These are the other four
// decisions, each asserted on which side of that line it falls.

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// An incomplete subject means the source adapter has a bug, and no amount of
// redelivery will grow the missing field — so it is skipped rather than
// retried forever. Each field is asserted separately: the guard is a
// three-way disjunction, and dropping any one arm lets an event through to be
// looked up with an empty collection or key, which matches nothing but is
// indistinguishable from a genuine miss.
func TestHandle_IncompleteSubjectIsSkippedNotRetried(t *testing.T) {
	tenant := uuid.New().String()

	cases := map[string]SubjectFields{
		"no tenant":     {Collection: "docs", Key: "a.txt"},
		"no collection": {TenantID: tenant, Key: "a.txt"},
		"no key":        {TenantID: tenant, Collection: "docs"},
		"none of it":    {},
	}
	for name, sf := range cases {
		t.Run(name, func(t *testing.T) {
			f := &fakeLookup{}
			h := &PromoteHandler{Lookup: f, Logger: zap.NewNop()}

			if err := h.Handle(context.Background(), CloudEvent{ID: "e", Type: EventTypeUploaded, SubjectFields: sf}); err != nil {
				t.Fatalf("Handle = %v, want nil — a malformed subject cannot be fixed by redelivery", err)
			}
			if f.lookupCalled {
				t.Errorf("looked the object up with collection=%q key=%q; an incomplete subject must not reach the lookup",
					f.gotCollection, f.gotKey)
			}
		})
	}
}

// A tenant id that is not a uuid is the same class of defect and takes the
// same exit. Asserted separately because it sits past the subject guard: a
// mutation that skipped the parse would reach pgtype.UUID with garbage.
func TestHandle_InvalidTenantIsSkippedNotRetried(t *testing.T) {
	f := &fakeLookup{}
	h := &PromoteHandler{Lookup: f, Logger: zap.NewNop()}

	ev := CloudEvent{ID: "e", Type: EventTypeUploaded, SubjectFields: SubjectFields{
		TenantID: "not-a-uuid", Collection: "docs", Key: "a.txt",
	}}
	if err := h.Handle(context.Background(), ev); err != nil {
		t.Fatalf("Handle = %v, want nil", err)
	}
	if f.lookupCalled {
		t.Error("an unparseable tenant id reached the object lookup")
	}
}

// The other side of the line. A database error while resolving the prefix is
// transient — the collection exists, we could not read it — so Handle must
// return the error and let the broker redeliver. Swallowing it drops a real
// upload: the object stays PENDING and only the reconciler will notice, hours
// later.
func TestHandle_PrefixResolveDBErrorIsRetried(t *testing.T) {
	boom := errors.New("connection reset")
	f := &fakeLookup{resolveErr: boom}
	h := &PromoteHandler{Lookup: f, Logger: zap.NewNop()}

	err := h.Handle(context.Background(), uploadedEvent(uuid.New(), "docs", "a.txt"))
	if err == nil {
		t.Fatal("Handle returned nil on a database error — the event is acked and lost")
	}
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want it to wrap the database error", err)
	}
	if f.lookupCalled {
		t.Error("continued to the object lookup after failing to resolve the prefix")
	}
}

// lookupErrOnly returns a chosen error from LookupObjectByKey and resolves the
// prefix cleanly, isolating the second skip-vs-retry decision.
type lookupErrOnly struct {
	fakeLookup
	err error
}

func (l *lookupErrOnly) LookupObjectByKey(_ context.Context, _ pgtype.UUID, collection, key string) (sqlc.LookupObjectByKeyRow, error) {
	l.lookupCalled = true
	l.gotCollection, l.gotKey = collection, key
	return sqlc.LookupObjectByKeyRow{}, l.err
}

func TestHandle_ObjectLookupSkipsMissesButRetriesFailures(t *testing.T) {
	boom := errors.New("connection reset")

	t.Run("no such object is skipped", func(t *testing.T) {
		// The race the comment describes: the storage event beat the
		// data-plane PUT's commit. Redelivery would find the same nothing,
		// and the reconciler covers the orphan.
		l := &lookupErrOnly{err: pgx.ErrNoRows}
		l.resolveErr = pgx.ErrNoRows
		h := &PromoteHandler{Lookup: l, Logger: zap.NewNop()}
		if err := h.Handle(context.Background(), uploadedEvent(uuid.New(), "docs", "a.txt")); err != nil {
			t.Fatalf("Handle = %v, want nil for a missing object", err)
		}
	})

	t.Run("a database failure is retried", func(t *testing.T) {
		l := &lookupErrOnly{err: boom}
		l.resolveErr = pgx.ErrNoRows
		h := &PromoteHandler{Lookup: l, Logger: zap.NewNop()}
		err := h.Handle(context.Background(), uploadedEvent(uuid.New(), "docs", "a.txt"))
		if err == nil {
			t.Fatal("Handle returned nil on a lookup failure — the event is acked and lost")
		}
		if !errors.Is(err, boom) {
			t.Errorf("err = %v, want it to wrap the database error", err)
		}
	})
}

// objectResourceName degrades to the C-shape name on any resolve miss, so a
// blip in the binding lookup never blocks the event. Three ways to miss, and
// each must land on the same fallback rather than emitting half a name.
func TestObjectResourceName_FallsBackToCShape(t *testing.T) {
	tenant := uuid.New().String()
	const (
		collection = "docs"
		key        = "a.txt"
	)
	cShape := "tenants/" + tenant + "/collections/" + collection + "/objects-by-key/" + key

	t.Run("resolved binding gives the canonical name", func(t *testing.T) {
		f := &fakeLookup{binding: sqlc.GetCollectionRow{BackendName: "primary", BucketName: "bkt"}}
		h := &PromoteHandler{Lookup: f, Logger: zap.NewNop()}
		want := "storageBackends/primary/buckets/bkt/" + cShape
		if got := h.objectResourceName(context.Background(), tenant, collection, key); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})

	for name, f := range map[string]*fakeLookup{
		"lookup failed":  {bindingErr: errors.New("connection reset")},
		"no backend":     {binding: sqlc.GetCollectionRow{BucketName: "bkt"}},
		"no bucket":      {binding: sqlc.GetCollectionRow{BackendName: "primary"}},
		"nothing at all": {},
	} {
		t.Run(name, func(t *testing.T) {
			h := &PromoteHandler{Lookup: f, Logger: zap.NewNop()}
			if got := h.objectResourceName(context.Background(), tenant, collection, key); got != cShape {
				t.Errorf("got %q, want the C-shape fallback %q", got, cShape)
			}
		})
	}

	t.Run("unparseable tenant also falls back", func(t *testing.T) {
		f := &fakeLookup{binding: sqlc.GetCollectionRow{BackendName: "primary", BucketName: "bkt"}}
		h := &PromoteHandler{Lookup: f, Logger: zap.NewNop()}
		want := "tenants/nope/collections/" + collection + "/objects-by-key/" + key
		if got := h.objectResourceName(context.Background(), "nope", collection, key); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	})
}

// A handler wired without an event producer still promotes; it just tells
// nobody. Emitting into a nil producer would panic on the promote tx.
func TestEmitUploaded_NilProducerIsANoOp(t *testing.T) {
	h := &PromoteHandler{Lookup: &fakeLookup{}, Logger: zap.NewNop()}
	if err := h.emitUploaded(context.Background(), nil, CloudEvent{}, "rn", "docs", "a.txt", uuid.New()); err != nil {
		t.Errorf("emitUploaded with no producer = %v, want nil", err)
	}
}

// tenantRecorder captures the pgtype.UUID the handler passes down, which the
// other fakes discard.
type tenantRecorder struct {
	fakeLookup
	gotLookupTenant pgtype.UUID
	gotBindTenant   pgtype.UUID
}

func (r *tenantRecorder) LookupObjectByKey(_ context.Context, t pgtype.UUID, collection, key string) (sqlc.LookupObjectByKeyRow, error) {
	r.lookupCalled = true
	r.gotLookupTenant = t
	r.gotCollection, r.gotKey = collection, key
	return sqlc.LookupObjectByKeyRow{}, pgx.ErrNoRows
}

func (r *tenantRecorder) GetCollection(_ context.Context, t pgtype.UUID, _ string) (sqlc.GetCollectionRow, error) {
	r.gotBindTenant = t
	return r.binding, r.bindingErr
}

// The tenant has to reach the queries as a *valid* pgtype.UUID. Marked
// invalid it is SQL NULL, and `WHERE tenant_id = $1` matches nothing — so
// every event in the deployment resolves to "no such object" and is skipped
// as the documented upload race. The pipeline stays green while ingesting
// nothing at all.
func TestHandle_PassesAValidTenantDown(t *testing.T) {
	tenant := uuid.New()
	r := &tenantRecorder{}
	r.resolveErr = pgx.ErrNoRows
	h := &PromoteHandler{Lookup: r, Logger: zap.NewNop()}

	if err := h.Handle(context.Background(), uploadedEvent(tenant, "docs", "a.txt")); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if !r.gotLookupTenant.Valid {
		t.Fatal("object lookup received an invalid tenant uuid — SQL NULL matches no row")
	}
	if uuid.UUID(r.gotLookupTenant.Bytes) != tenant {
		t.Errorf("lookup tenant = %v, want %v", uuid.UUID(r.gotLookupTenant.Bytes), tenant)
	}
}

func TestObjectResourceName_PassesAValidTenantDown(t *testing.T) {
	tenant := uuid.New()
	r := &tenantRecorder{}
	r.binding = sqlc.GetCollectionRow{BackendName: "primary", BucketName: "bkt"}
	h := &PromoteHandler{Lookup: r, Logger: zap.NewNop()}

	_ = h.objectResourceName(context.Background(), tenant.String(), "docs", "a.txt")
	if !r.gotBindTenant.Valid {
		t.Fatal("binding lookup received an invalid tenant uuid")
	}
	if uuid.UUID(r.gotBindTenant.Bytes) != tenant {
		t.Errorf("binding tenant = %v, want %v", uuid.UUID(r.gotBindTenant.Bytes), tenant)
	}
}

// The first switch arm needs all three conditions. Relaxed to a disjunction,
// a clean resolve that matched no prefix — ("", nil), which is what the query
// returns for a tenant with no collection covering the tail — takes the
// rewrite branch instead of the keep-the-split one, and the collection is
// blanked to "" while the key keeps a leading segment. The lookup then misses
// for a reason that looks identical to an unknown object.
func TestHandle_EmptyPrefixWithNoErrorKeepsSourceSplit(t *testing.T) {
	f := &fakeLookup{resolveReturn: "", resolveErr: nil}
	h := &PromoteHandler{Lookup: f, Logger: zap.NewNop()}

	if err := h.Handle(context.Background(), uploadedEvent(uuid.New(), "docs", "a.txt")); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	if f.gotCollection != "docs" || f.gotKey != "a.txt" {
		t.Errorf("split = (%q, %q), want the source's (docs, a.txt)", f.gotCollection, f.gotKey)
	}
}

// A handler wired without a logger is the shape every other component in this
// package tolerates, and Handle reaches for one on its first line.
func TestHandle_NilLoggerIsSafe(t *testing.T) {
	f := &fakeLookup{resolveErr: pgx.ErrNoRows}
	h := &PromoteHandler{Lookup: f} // no Logger
	if err := h.Handle(context.Background(), uploadedEvent(uuid.New(), "docs", "a.txt")); err != nil {
		t.Fatalf("Handle with a nil logger: %v", err)
	}
}
