package objecth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
	"github.com/oleg-tkachuk/paladin/capability"
	"github.com/oleg-tkachuk/paladin/capability/memstore"
)

// The tail of CompleteObject and CopyObject — everything after the state
// transition succeeds — used to be unreachable from a unit test because the
// handler held a concrete *statemachine.Transitioner and a real transition
// needs a Postgres pool. It holds the StateMachine interface now, so the
// three things that happen only on a REAL transition can be pinned down:
// version history, quota accounting and the capability charge. All three are
// guarded by `changed`, which is exactly what makes an at-least-once client
// retry cost nothing — a retry that re-recorded a version or re-charged the
// caller would be a billing bug, not a cosmetic one.
//
// The fake also runs the onPromoted callback the way the real transitioner
// does (only when changed, inside what would be the tx), so the outbox event
// each path enqueues is observable here too — including the `source` field
// that tells a copy apart from a direct upload.

type fakeStateMachine struct {
	changed    bool
	promoteErr error

	promotedID   uuid.UUID
	promotedETag string
	promotedSize int64
	promoteCalls int

	markedFailed []uuid.UUID
	markFailedBy []string
	markFailErr  error
}

func (f *fakeStateMachine) PromoteToAvailableInTx(
	ctx context.Context, objectID uuid.UUID, etag string, sizeBytes int64,
	_, _ string, _ statemachine.Source,
	onPromoted func(context.Context, pgx.Tx) error,
) (bool, error) {
	f.promoteCalls++
	f.promotedID, f.promotedETag, f.promotedSize = objectID, etag, sizeBytes
	if f.promoteErr != nil {
		return false, f.promoteErr
	}
	// Mirrors the real contract: the callback fires only on a real
	// transition, and its error fails the whole promote.
	if f.changed && onPromoted != nil {
		if err := onPromoted(ctx, nil); err != nil {
			return false, err
		}
	}
	return f.changed, nil
}

func (f *fakeStateMachine) MarkFailed(_ context.Context, objectID uuid.UUID, reason string) error {
	f.markedFailed = append(f.markedFailed, objectID)
	f.markFailedBy = append(f.markFailedBy, reason)
	return f.markFailErr
}

func (*fakeStateMachine) PromoteToAvailable(context.Context, uuid.UUID, string, int64, string, string, statemachine.Source) (bool, error) {
	panic("not used")
}
func (*fakeStateMachine) SoftDeleteInTx(context.Context, uuid.UUID, int64, func(context.Context, pgx.Tx) error) error {
	panic("not used")
}
func (*fakeStateMachine) RestoreInTx(context.Context, uuid.UUID, func(context.Context, pgx.Tx) error) error {
	panic("not used")
}

// failCopyStorage answers HEAD like headStorage but refuses the server-side
// copy, which is the only way to reach CopyObject's compensation.
type failCopyStorage struct {
	headStorage
	copyErr error
}

func (s failCopyStorage) CopyObject(context.Context, Location, Location) error { return s.copyErr }

// tailHandler wires the collaborators the tail actually touches. versioning
// is on so OnPromote does something observable rather than returning early.
func tailHandler(t *testing.T, repo *completeRepo, st Storage, sm *fakeStateMachine) (
	*Handler, *fakeQuota, *fakeVersionRepo, *fakeEvents, context.Context, uuid.UUID,
) {
	t.Helper()
	tenantID := uuid.New()
	ctx := auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u1", TenantID: tenantID})

	repo.meta = BucketMeta{BackendID: "backend-7", BucketName: "bucket-7", VersioningEnabled: true}
	quota := &fakeQuota{}
	versions := newFakeVersionRepo()
	events := &fakeEvents{}

	h := &Handler{
		repo: repo, storage: st, policy: &recordingAuthorizer{}, sm: sm,
		presign: PresignConfig{DefaultTTL: time.Hour, MaxTTL: 2 * time.Hour},
	}
	h.SetVersionHandler(NewVersionHandler(repo, versions))
	h.SetQuotaUpdater(quota)
	h.SetEventProducer(events)
	return h, quota, versions, events, ctx, tenantID
}

func pendingObject(tenantID uuid.UUID) Object {
	return Object{
		ObjectID: uuid.Must(uuid.NewV7()), TenantID: tenantID,
		Collection: "docs", Key: "a.txt", State: statemachine.StatePending,
		ContentType: "text/plain",
	}
}

// ─── CompleteObject tail ────────────────────────────────────────────────────

func TestCompleteObjectTailRecordsVersionQuotaAndEventOnARealPromote(t *testing.T) {
	sm := &fakeStateMachine{changed: true}
	repo := &completeRepo{}
	h, quota, versions, events, ctx, tenantID := tailHandler(t, repo, headStorage{etag: "e1", size: 12}, sm)
	obj := pendingObject(tenantID)
	repo.obj = obj

	got, err := h.CompleteObject(ctx, CompleteObjectInput{Collection: "docs", ObjectID: obj.ObjectID.String()})
	if err != nil {
		t.Fatalf("CompleteObject: %v", err)
	}
	if got.ObjectID != obj.ObjectID {
		t.Errorf("returned %v, want the re-read object %v", got.ObjectID, obj.ObjectID)
	}

	// The authoritative values come from HEAD, not from the client.
	if sm.promotedETag != "e1" || sm.promotedSize != 12 {
		t.Errorf("promoted with etag=%q size=%d, want the HEAD values e1/12", sm.promotedETag, sm.promotedSize)
	}
	if len(versions.inserted) != 1 {
		t.Fatalf("recorded %d versions, want 1", len(versions.inserted))
	}
	if versions.inserted[0].ObjectID != obj.ObjectID {
		t.Error("the version row points at a different object")
	}
	if quota.calls != 1 {
		t.Errorf("quota charged %d times, want 1", quota.calls)
	}
	if len(events.evts) != 1 || events.evts[0].Type != "paladin.object.uploaded" {
		t.Fatalf("events = %+v, want one paladin.object.uploaded", events.evts)
	}
}

func TestCompleteObjectTailIsSilentWhenNothingChanged(t *testing.T) {
	// changed=false is the shape of a retry that lost the race to the
	// event-driven promote. The object is already AVAILABLE and correct;
	// re-recording a version or re-charging quota would be double counting.
	sm := &fakeStateMachine{changed: false}
	repo := &completeRepo{}
	h, quota, versions, events, ctx, tenantID := tailHandler(t, repo, headStorage{etag: "e1", size: 12}, sm)
	repo.obj = pendingObject(tenantID)

	if _, err := h.CompleteObject(ctx, CompleteObjectInput{
		Collection: "docs", ObjectID: repo.obj.ObjectID.String(),
	}); err != nil {
		t.Fatalf("CompleteObject: %v", err)
	}
	if len(versions.inserted) != 0 {
		t.Errorf("recorded %d versions on a no-op promote, want 0", len(versions.inserted))
	}
	if quota.calls != 0 {
		t.Errorf("quota charged %d times on a no-op promote, want 0", quota.calls)
	}
	if len(events.evts) != 0 {
		t.Errorf("emitted %d events on a no-op promote, want 0", len(events.evts))
	}
}

func TestCompleteObjectTailFailsWhenTheEventCannotBeEnqueued(t *testing.T) {
	// ADR-0003: the outbox write rides the promote's transaction, so a
	// dispatch failure must fail the RPC rather than leaving the object
	// AVAILABLE with nobody told.
	sm := &fakeStateMachine{changed: true}
	repo := &completeRepo{}
	h, quota, versions, events, ctx, tenantID := tailHandler(t, repo, headStorage{etag: "e1", size: 12}, sm)
	repo.obj = pendingObject(tenantID)
	events.err = errors.New("outbox down")

	_, err := h.CompleteObject(ctx, CompleteObjectInput{
		Collection: "docs", ObjectID: repo.obj.ObjectID.String(),
	})
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("code = %v, want Internal", connect.CodeOf(err))
	}
	if len(versions.inserted) != 0 || quota.calls != 0 {
		t.Error("the tail ran despite the promote being rolled back")
	}
}

func TestCompleteObjectTailSurfacesAPromoteFailure(t *testing.T) {
	sm := &fakeStateMachine{promoteErr: errors.New("deadlock")}
	repo := &completeRepo{}
	h, _, _, _, ctx, tenantID := tailHandler(t, repo, headStorage{etag: "e1", size: 12}, sm)
	repo.obj = pendingObject(tenantID)

	_, err := h.CompleteObject(ctx, CompleteObjectInput{
		Collection: "docs", ObjectID: repo.obj.ObjectID.String(),
	})
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("code = %v, want Internal", connect.CodeOf(err))
	}
}

// ─── CopyObject tail ────────────────────────────────────────────────────────

func copySource(tenantID uuid.UUID) Object {
	src := availableSource()
	src.TenantID = tenantID
	src.ETag = "src-etag"
	return src
}

func TestCopyObjectTailEmitsAnUploadedEventMarkedAsACopy(t *testing.T) {
	// A copy materialises a new object, so subscribers get the same
	// paladin.object.uploaded they'd get from an upload. The `source`
	// discriminator is the only thing that lets a consumer tell them apart,
	// which makes it part of the contract rather than a debugging aid.
	sm := &fakeStateMachine{changed: true}
	repo := &completeRepo{}
	h, quota, versions, events, ctx, tenantID := tailHandler(t, repo, noopStorage{}, sm)
	repo.obj = copySource(tenantID)
	srcID := uuid.NewString()

	if _, err := h.CopyObject(ctx, CopyObjectInput{
		SourceCollection: "src", SourceObjectID: srcID,
		DestCollection: "dst", DestKey: "copy.txt",
	}); err != nil {
		t.Fatalf("CopyObject: %v", err)
	}
	if len(events.evts) != 1 {
		t.Fatalf("emitted %d events, want 1", len(events.evts))
	}
	payload := events.evts[0].Payload
	if payload["source"] != "copy" {
		t.Errorf("source = %v, want \"copy\"", payload["source"])
	}
	if payload["source_collection"] != "src" || payload["source_object_id"] != srcID {
		t.Errorf("payload does not name the origin: %v", payload)
	}
	if payload["key"] != "copy.txt" {
		t.Errorf("key = %v, want the destination key", payload["key"])
	}
	// The copy inherits the source's bytes, so it is promoted with the
	// source's etag rather than a fresh HEAD.
	if sm.promotedETag != "src-etag" {
		t.Errorf("promoted with etag %q, want the source's", sm.promotedETag)
	}
	if len(versions.inserted) != 1 || quota.calls != 1 {
		t.Errorf("versions=%d quota=%d, want 1 and 1", len(versions.inserted), quota.calls)
	}
}

func TestCopyObjectTailSkipsAccountingWhenNothingChanged(t *testing.T) {
	sm := &fakeStateMachine{changed: false}
	repo := &completeRepo{}
	h, quota, versions, _, ctx, tenantID := tailHandler(t, repo, noopStorage{}, sm)
	repo.obj = copySource(tenantID)

	if _, err := h.CopyObject(ctx, CopyObjectInput{
		SourceCollection: "src", SourceObjectID: uuid.NewString(), DestCollection: "dst",
	}); err != nil {
		t.Fatalf("CopyObject: %v", err)
	}
	if len(versions.inserted) != 0 || quota.calls != 0 {
		t.Errorf("versions=%d quota=%d on a no-op promote, want 0 and 0", len(versions.inserted), quota.calls)
	}
}

func TestCopyObjectCompensatesWhenTheStorageCopyFails(t *testing.T) {
	// The destination row is inserted PENDING before the bytes move. If the
	// copy fails it must be marked FAILED here: nothing else will ever
	// promote it, because the reconciler promotes by HEADing an object the
	// failed copy never wrote.
	sm := &fakeStateMachine{}
	repo := &completeRepo{}
	st := failCopyStorage{copyErr: errors.New("s3 refused")}
	h, _, _, _, ctx, tenantID := tailHandler(t, repo, st, sm)
	repo.obj = copySource(tenantID)

	_, err := h.CopyObject(ctx, CopyObjectInput{
		SourceCollection: "src", SourceObjectID: uuid.NewString(), DestCollection: "dst",
	})
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("code = %v, want Internal", connect.CodeOf(err))
	}
	if len(sm.markedFailed) != 1 {
		t.Fatalf("MarkFailed called %d times, want 1 — the destination row would linger PENDING forever", len(sm.markedFailed))
	}
	if repo.created == nil || sm.markedFailed[0] == repo.obj.ObjectID {
		t.Error("compensation marked the wrong object — it must be the DESTINATION, not the source")
	}
	if sm.promoteCalls != 0 {
		t.Error("promoted despite the bytes never arriving")
	}
}

func TestCopyObjectReportsBothFailuresWhenCompensationAlsoFails(t *testing.T) {
	// Losing the compensation error would leave an operator looking for a
	// PENDING row with no explanation of why it is stuck.
	sm := &fakeStateMachine{markFailErr: errors.New("db gone")}
	repo := &completeRepo{}
	st := failCopyStorage{copyErr: errors.New("s3 refused")}
	h, _, _, _, ctx, tenantID := tailHandler(t, repo, st, sm)
	repo.obj = copySource(tenantID)

	_, err := h.CopyObject(ctx, CopyObjectInput{
		SourceCollection: "src", SourceObjectID: uuid.NewString(), DestCollection: "dst",
	})
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("code = %v, want Internal", connect.CodeOf(err))
	}
	if !strings.Contains(err.Error(), "s3 refused") || !strings.Contains(err.Error(), "db gone") {
		t.Errorf("error = %q, want both the copy failure and the compensation failure", err)
	}
}

// ─── the capability charge ──────────────────────────────────────────────────

// A copy is gated by OpPut and produces a new stored object, so it spends the
// holder's budget like any other write. It did not, which made CopyObject the
// one OpPut path a budgeted principal could use for free — the budget stops
// meaning "how much this principal may store" the moment one way of storing
// is exempt.

func budgetedCtx(t *testing.T, tenantID uuid.UUID, maxBudget float64) (context.Context, *capability.Capability, *memstore.UsageStore[pgx.Tx]) {
	t.Helper()
	cap := &capability.Capability{
		ID: uuid.New(),
		// Ops must name put or the gate refuses before the charge is
		// reached; no prefixes means every URI is in scope.
		Caveats: capability.Caveats{
			Ops:             []capability.Op{capability.OpPut},
			MaxBudgetAmount: maxBudget,
		},
	}
	usage := memstore.NewUsage[pgx.Tx](nil)
	ctx := auth.WithPrincipal(context.Background(), &auth.Principal{Subject: "u1", TenantID: tenantID})
	ctx = auth.WithChargeAmount(auth.WithChargeStore(auth.WithCapability(ctx, cap), usage), 1, "")
	return ctx, cap, usage
}

func TestCopyObjectChargesTheCapabilityBudget(t *testing.T) {
	sm := &fakeStateMachine{changed: true}
	repo := &completeRepo{}
	h, _, _, _, _, tenantID := tailHandler(t, repo, noopStorage{}, sm)
	repo.obj = copySource(tenantID)
	ctx, cap, usage := budgetedCtx(t, tenantID, 10)

	if _, err := h.CopyObject(ctx, CopyObjectInput{
		SourceCollection: "src", SourceObjectID: uuid.NewString(), DestCollection: "dst",
	}); err != nil {
		t.Fatalf("CopyObject: %v", err)
	}
	u, err := usage.Get(ctx, cap.ID)
	if err != nil {
		t.Fatalf("no usage recorded — the copy was free: %v", err)
	}
	if u.SpentAmount != 1 {
		t.Errorf("spent = %v, want 1", u.SpentAmount)
	}
}

func TestCopyObjectRefusesWhenTheBudgetIsSpent(t *testing.T) {
	sm := &fakeStateMachine{changed: true}
	repo := &completeRepo{}
	h, _, _, _, _, tenantID := tailHandler(t, repo, noopStorage{}, sm)
	repo.obj = copySource(tenantID)
	ctx, _, _ := budgetedCtx(t, tenantID, 1)

	in := CopyObjectInput{SourceCollection: "src", SourceObjectID: uuid.NewString(), DestCollection: "dst"}
	if _, err := h.CopyObject(ctx, in); err != nil {
		t.Fatalf("the first copy is within budget: %v", err)
	}
	if _, err := h.CopyObject(ctx, in); err == nil {
		t.Fatal("a second copy over budget was allowed")
	}
}

func TestCopyObjectDoesNotChargeARetryThatChangedNothing(t *testing.T) {
	sm := &fakeStateMachine{changed: false}
	repo := &completeRepo{}
	h, _, _, _, _, tenantID := tailHandler(t, repo, noopStorage{}, sm)
	repo.obj = copySource(tenantID)
	ctx, cap, usage := budgetedCtx(t, tenantID, 10)

	if _, err := h.CopyObject(ctx, CopyObjectInput{
		SourceCollection: "src", SourceObjectID: uuid.NewString(), DestCollection: "dst",
	}); err != nil {
		t.Fatalf("CopyObject: %v", err)
	}
	if _, err := usage.Get(ctx, cap.ID); err == nil {
		t.Error("charged a promote that changed nothing — an at-least-once retry would bill twice")
	}
}
