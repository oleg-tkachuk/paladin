package operations

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/batch"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/object"
	"github.com/oleg-tkachuk/paladin/internal/api/v1/operation"
	"github.com/oleg-tkachuk/paladin/internal/statemachine"
)

// Covers the BatchDelete executor's contract: partial success (one bad object
// must not sink the batch), the tenant-mismatch defence, and the fresh
// resource_version handed to the optimistic-concurrency check.

// ─── fakes ─────────────────────────────────────────────────────────────────

// lookupRepo implements just the read half of object.Repository that the batch
// executors use; the rest is embedded so an unexpected call panics.
type lookupRepo struct {
	object.Repository
	objs []object.Object
	err  error

	gotTenant uuid.UUID
	gotIDs    []uuid.UUID
}

func (r *lookupRepo) FindByIDs(_ context.Context, tenantID uuid.UUID, ids []uuid.UUID) ([]object.Object, error) {
	r.gotTenant, r.gotIDs = tenantID, ids
	return r.objs, r.err
}

type fakeTransitions struct {
	Transitioner
	softErrs map[uuid.UUID]error
	softArgs map[uuid.UUID]int64
	restored map[uuid.UUID]bool
	restErrs map[uuid.UUID]error
}

func newFakeTransitions() *fakeTransitions {
	return &fakeTransitions{
		softErrs: map[uuid.UUID]error{},
		softArgs: map[uuid.UUID]int64{},
		restored: map[uuid.UUID]bool{},
		restErrs: map[uuid.UUID]error{},
	}
}

func (f *fakeTransitions) SoftDelete(_ context.Context, id uuid.UUID, rv int64) error {
	f.softArgs[id] = rv
	return f.softErrs[id]
}

func (f *fakeTransitions) Restore(_ context.Context, id uuid.UUID) error {
	f.restored[id] = true
	return f.restErrs[id]
}

func (f *fakeTransitions) MarkFailed(context.Context, uuid.UUID, string) error { return nil }

func (f *fakeTransitions) PromoteToAvailable(context.Context, uuid.UUID, string, int64, string, string, statemachine.Source) (bool, error) {
	return false, nil
}

func mkOp(t *testing.T, tenantID uuid.UUID, args any) operation.Operation {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatalf("marshal metadata: %v", err)
	}
	return operation.Operation{TenantID: tenantID, Metadata: raw}
}

func decodeDeleteResp(t *testing.T, body []byte) BatchDeleteResponse {
	t.Helper()
	var resp BatchDeleteResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}

// ─── dependency + metadata validation ──────────────────────────────────────

func TestBatchDeleteRequiresDependencies(t *testing.T) {
	tenant := uuid.New()
	op := mkOp(t, tenant, batch.BatchDeleteArgs{TenantID: tenant, Collection: "k", ObjectIDs: []uuid.UUID{uuid.New()}})

	for name, e := range map[string]*BatchDeleteExecutor{
		"no repo":        {Transitions: newFakeTransitions()},
		"no transitions": {Objects: &lookupRepo{}},
		"neither":        {},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := e.Execute(context.Background(), op); err == nil {
				t.Error("want a wiring error")
			}
		})
	}
}

func TestBatchDeleteRejectsBadMetadata(t *testing.T) {
	tenant := uuid.New()
	e := &BatchDeleteExecutor{Objects: &lookupRepo{}, Transitions: newFakeTransitions()}

	t.Run("undecodable", func(t *testing.T) {
		op := operation.Operation{TenantID: tenant, Metadata: []byte("not-json")}
		if _, err := e.Execute(context.Background(), op); err == nil {
			t.Error("want a decode error")
		}
	})

	for name, args := range map[string]batch.BatchDeleteArgs{
		"no tenant":     {Collection: "k", ObjectIDs: []uuid.UUID{uuid.New()}},
		"no object key": {TenantID: tenant, ObjectIDs: []uuid.UUID{uuid.New()}},
		"no ids":        {TenantID: tenant, Collection: "k"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := e.Execute(context.Background(), mkOp(t, tenant, args)); err == nil {
				t.Error("want a validation error")
			}
		})
	}
}

// The handler stamps the tenant from the auth context AND embeds it in
// metadata; a disagreement means the row was tampered with, so the executor
// must refuse rather than act on the metadata's tenant.
func TestBatchDeleteRefusesTenantMismatch(t *testing.T) {
	opTenant, metaTenant := uuid.New(), uuid.New()
	repo := &lookupRepo{}
	e := &BatchDeleteExecutor{Objects: repo, Transitions: newFakeTransitions()}

	op := mkOp(t, opTenant, batch.BatchDeleteArgs{
		TenantID: metaTenant, Collection: "k", ObjectIDs: []uuid.UUID{uuid.New()},
	})

	_, err := e.Execute(context.Background(), op)
	if err == nil {
		t.Fatal("want a tenant-mismatch error")
	}
	if repo.gotIDs != nil {
		t.Error("a tampered row must not reach the repository")
	}
}

// ─── happy path + partial success ──────────────────────────────────────────

func TestBatchDeleteSucceeds(t *testing.T) {
	tenant := uuid.New()
	id1, id2 := uuid.New(), uuid.New()
	repo := &lookupRepo{objs: []object.Object{
		{ObjectID: id1, ResourceVersion: 3},
		{ObjectID: id2, ResourceVersion: 7},
	}}
	tr := newFakeTransitions()
	e := &BatchDeleteExecutor{Objects: repo, Transitions: tr}

	body, err := e.Execute(context.Background(), mkOp(t, tenant, batch.BatchDeleteArgs{
		TenantID: tenant, Collection: "k", ObjectIDs: []uuid.UUID{id1, id2},
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	resp := decodeDeleteResp(t, body)
	if resp.Total != 2 || resp.Succeeded != 2 || resp.Failed != 0 {
		t.Errorf("counts = %+v", resp)
	}
	// Each row's CURRENT version must reach SoftDelete, or a concurrent update
	// would be silently overwritten instead of surfacing as a mismatch.
	if tr.softArgs[id1] != 3 || tr.softArgs[id2] != 7 {
		t.Errorf("resource versions = %v", tr.softArgs)
	}
	// The read phase must be ONE batched query for the whole id list.
	if len(repo.gotIDs) != 2 || repo.gotTenant != tenant {
		t.Errorf("lookup args = %v / %v", repo.gotTenant, repo.gotIDs)
	}
}

// Partial success is the batch contract: a missing id is reported per-object,
// not raised as a batch-level error.
func TestBatchDeleteReportsMissingObjects(t *testing.T) {
	tenant := uuid.New()
	present, missing := uuid.New(), uuid.New()
	repo := &lookupRepo{objs: []object.Object{{ObjectID: present, ResourceVersion: 1}}}
	e := &BatchDeleteExecutor{Objects: repo, Transitions: newFakeTransitions()}

	body, err := e.Execute(context.Background(), mkOp(t, tenant, batch.BatchDeleteArgs{
		TenantID: tenant, Collection: "k", ObjectIDs: []uuid.UUID{present, missing},
	}))
	if err != nil {
		t.Fatalf("a missing object must not fail the batch: %v", err)
	}

	resp := decodeDeleteResp(t, body)
	if resp.Succeeded != 1 || resp.Failed != 1 {
		t.Errorf("counts = %+v", resp)
	}
	if len(resp.Failures) != 1 || resp.Failures[0].ObjectID != missing.String() {
		t.Fatalf("failures = %+v", resp.Failures)
	}
	// The reason is what lets a client retry just the failures.
	if resp.Failures[0].Reason != "not found" {
		t.Errorf("reason = %q", resp.Failures[0].Reason)
	}
}

func TestBatchDeleteReportsTransitionFailures(t *testing.T) {
	tenant := uuid.New()
	ok, bad := uuid.New(), uuid.New()
	repo := &lookupRepo{objs: []object.Object{
		{ObjectID: ok, ResourceVersion: 1}, {ObjectID: bad, ResourceVersion: 1},
	}}
	tr := newFakeTransitions()
	tr.softErrs[bad] = statemachine.ErrConflict
	e := &BatchDeleteExecutor{Objects: repo, Transitions: tr}

	body, err := e.Execute(context.Background(), mkOp(t, tenant, batch.BatchDeleteArgs{
		TenantID: tenant, Collection: "k", ObjectIDs: []uuid.UUID{ok, bad},
	}))
	if err != nil {
		t.Fatalf("a per-object failure must not fail the batch: %v", err)
	}

	resp := decodeDeleteResp(t, body)
	if resp.Succeeded != 1 || resp.Failed != 1 {
		t.Errorf("counts = %+v", resp)
	}
	if len(resp.Failures) != 1 || resp.Failures[0].ObjectID != bad.String() {
		t.Fatalf("failures = %+v", resp.Failures)
	}
	if resp.Failures[0].Reason == "" {
		t.Error("the underlying reason must be surfaced")
	}
}

// A lookup failure is batch-level: nothing can be attempted, so it must be
// raised rather than reported as N per-object failures.
func TestBatchDeleteLookupErrorFailsTheBatch(t *testing.T) {
	tenant := uuid.New()
	repo := &lookupRepo{err: errors.New("db down")}
	e := &BatchDeleteExecutor{Objects: repo, Transitions: newFakeTransitions()}

	_, err := e.Execute(context.Background(), mkOp(t, tenant, batch.BatchDeleteArgs{
		TenantID: tenant, Collection: "k", ObjectIDs: []uuid.UUID{uuid.New()},
	}))
	if err == nil {
		t.Fatal("want the lookup error")
	}
}

// A worker shutting down mid-batch must stop and surface the cancellation;
// rows already deleted stay committed.
func TestBatchDeleteHonoursContextCancellation(t *testing.T) {
	tenant := uuid.New()
	id := uuid.New()
	repo := &lookupRepo{objs: []object.Object{{ObjectID: id, ResourceVersion: 1}}}
	e := &BatchDeleteExecutor{Objects: repo, Transitions: newFakeTransitions()}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := e.Execute(ctx, mkOp(t, tenant, batch.BatchDeleteArgs{
		TenantID: tenant, Collection: "k", ObjectIDs: []uuid.UUID{id},
	}))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("want context.Canceled, got %v", err)
	}
}

// ─── findByIDs ─────────────────────────────────────────────────────────────

func TestFindByIDsIndexesByID(t *testing.T) {
	id1, id2 := uuid.New(), uuid.New()
	repo := &lookupRepo{objs: []object.Object{
		{ObjectID: id1, Key: "a"}, {ObjectID: id2, Key: "b"},
	}}

	got, err := findByIDs(context.Background(), repo, uuid.New(), []uuid.UUID{id1, id2})
	if err != nil {
		t.Fatalf("findByIDs: %v", err)
	}
	if len(got) != 2 || got[id1].Key != "a" || got[id2].Key != "b" {
		t.Errorf("index = %+v", got)
	}
}

// Ids the repository did not return must simply be absent, which is how
// callers detect "not found" without a second query.
func TestFindByIDsOmitsMissing(t *testing.T) {
	present, missing := uuid.New(), uuid.New()
	repo := &lookupRepo{objs: []object.Object{{ObjectID: present}}}

	got, err := findByIDs(context.Background(), repo, uuid.New(), []uuid.UUID{present, missing})
	if err != nil {
		t.Fatalf("findByIDs: %v", err)
	}
	if _, ok := got[missing]; ok {
		t.Error("a missing id must not appear in the index")
	}
}

func TestFindByIDsPropagatesError(t *testing.T) {
	boom := errors.New("db down")
	if _, err := findByIDs(context.Background(), &lookupRepo{err: boom}, uuid.New(), nil); !errors.Is(err, boom) {
		t.Fatalf("want the repo error, got %v", err)
	}
}

// ─── BatchRestore ──────────────────────────────────────────────────────────

func decodeRestoreResp(t *testing.T, body []byte) BatchRestoreResponse {
	t.Helper()
	var resp BatchRestoreResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}

func TestBatchRestoreSucceeds(t *testing.T) {
	tenant := uuid.New()
	id1, id2 := uuid.New(), uuid.New()
	repo := &lookupRepo{objs: []object.Object{{ObjectID: id1}, {ObjectID: id2}}}
	tr := newFakeTransitions()
	e := &BatchRestoreExecutor{Objects: repo, Transitions: tr}

	body, err := e.Execute(context.Background(), mkOp(t, tenant, batch.BatchRestoreObjectsArgs{
		TenantID: tenant, Collection: "k", ObjectIDs: []uuid.UUID{id1, id2},
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	resp := decodeRestoreResp(t, body)
	if resp.Total != 2 || resp.Succeeded != 2 || resp.Failed != 0 {
		t.Errorf("counts = %+v", resp)
	}
	if !tr.restored[id1] || !tr.restored[id2] {
		t.Errorf("both objects must be restored, got %v", tr.restored)
	}
}

func TestBatchRestorePartialSuccess(t *testing.T) {
	tenant := uuid.New()
	ok, bad, missing := uuid.New(), uuid.New(), uuid.New()
	repo := &lookupRepo{objs: []object.Object{{ObjectID: ok}, {ObjectID: bad}}}
	tr := newFakeTransitions()
	tr.restErrs[bad] = statemachine.ErrNotFound
	e := &BatchRestoreExecutor{Objects: repo, Transitions: tr}

	body, err := e.Execute(context.Background(), mkOp(t, tenant, batch.BatchRestoreObjectsArgs{
		TenantID: tenant, Collection: "k", ObjectIDs: []uuid.UUID{ok, bad, missing},
	}))
	if err != nil {
		t.Fatalf("partial failures must not sink the batch: %v", err)
	}
	resp := decodeRestoreResp(t, body)
	if resp.Total != 3 || resp.Succeeded != 1 || resp.Failed != 2 {
		t.Errorf("counts = %+v", resp)
	}
}

func TestBatchRestoreValidatesMetadata(t *testing.T) {
	tenant := uuid.New()
	e := &BatchRestoreExecutor{Objects: &lookupRepo{}, Transitions: newFakeTransitions()}

	t.Run("tenant mismatch", func(t *testing.T) {
		op := mkOp(t, tenant, batch.BatchRestoreObjectsArgs{
			TenantID: uuid.New(), Collection: "k", ObjectIDs: []uuid.UUID{uuid.New()},
		})
		if _, err := e.Execute(context.Background(), op); err == nil {
			t.Error("want a tenant-mismatch error")
		}
	})
	t.Run("missing dependencies", func(t *testing.T) {
		bare := &BatchRestoreExecutor{}
		op := mkOp(t, tenant, batch.BatchRestoreObjectsArgs{
			TenantID: tenant, Collection: "k", ObjectIDs: []uuid.UUID{uuid.New()},
		})
		if _, err := bare.Execute(context.Background(), op); err == nil {
			t.Error("want a wiring error")
		}
	})
	t.Run("undecodable metadata", func(t *testing.T) {
		op := operation.Operation{TenantID: tenant, Metadata: []byte("{")}
		if _, err := e.Execute(context.Background(), op); err == nil {
			t.Error("want a decode error")
		}
	})
}

// ─── BatchUpdateTags ───────────────────────────────────────────────────────

// tagRepo adds the write half used by the update-tags executor.
type tagRepo struct {
	lookupRepo
	updErrs map[uuid.UUID]error
	updArgs []object.UpdateMetadataArgs
}

func (r *tagRepo) UpdateMetadata(_ context.Context, args object.UpdateMetadataArgs) (object.Object, error) {
	r.updArgs = append(r.updArgs, args)
	if err := r.updErrs[args.ObjectID]; err != nil {
		return object.Object{}, err
	}
	return object.Object{ObjectID: args.ObjectID}, nil
}

func decodeTagsResp(t *testing.T, body []byte) BatchUpdateTagsResponse {
	t.Helper()
	var resp BatchUpdateTagsResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return resp
}

func TestBatchUpdateTagsSucceeds(t *testing.T) {
	tenant := uuid.New()
	id := uuid.New()
	repo := &tagRepo{
		lookupRepo: lookupRepo{objs: []object.Object{{ObjectID: id, ResourceVersion: 5}}},
		updErrs:    map[uuid.UUID]error{},
	}
	e := &BatchUpdateTagsExecutor{Objects: repo}

	body, err := e.Execute(context.Background(), mkOp(t, tenant, batch.BatchUpdateTagsArgs{
		TenantID: tenant, Collection: "k", ObjectIDs: []uuid.UUID{id},
		Tags: map[string]string{"env": "prod"},
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	resp := decodeTagsResp(t, body)
	if resp.Total != 1 || resp.Succeeded != 1 {
		t.Errorf("counts = %+v", resp)
	}
	if len(repo.updArgs) != 1 {
		t.Fatalf("want one update, got %d", len(repo.updArgs))
	}
	// The supplied map REPLACES the existing tags, and the row's current
	// version drives the optimistic-concurrency check.
	if repo.updArgs[0].Tags["env"] != "prod" {
		t.Errorf("tags = %v", repo.updArgs[0].Tags)
	}
	if repo.updArgs[0].ResourceVersion != 5 {
		t.Errorf("ResourceVersion = %d, want the row's current 5", repo.updArgs[0].ResourceVersion)
	}
	// Only the tags field may be touched — a wider mask would blank metadata
	// the caller never asked to change.
	if len(repo.updArgs[0].UpdatedFields) != 1 || repo.updArgs[0].UpdatedFields[0] != "tags" {
		t.Errorf("UpdatedFields = %v, want [tags]", repo.updArgs[0].UpdatedFields)
	}
}

func TestBatchUpdateTagsPartialSuccess(t *testing.T) {
	tenant := uuid.New()
	ok, bad, missing := uuid.New(), uuid.New(), uuid.New()
	repo := &tagRepo{
		lookupRepo: lookupRepo{objs: []object.Object{{ObjectID: ok}, {ObjectID: bad}}},
		updErrs:    map[uuid.UUID]error{bad: object.ErrVersionMismatch},
	}
	e := &BatchUpdateTagsExecutor{Objects: repo}

	body, err := e.Execute(context.Background(), mkOp(t, tenant, batch.BatchUpdateTagsArgs{
		TenantID: tenant, Collection: "k", ObjectIDs: []uuid.UUID{ok, bad, missing},
		Tags: map[string]string{"a": "1"},
	}))
	if err != nil {
		t.Fatalf("partial failures must not sink the batch: %v", err)
	}
	resp := decodeTagsResp(t, body)
	if resp.Total != 3 || resp.Succeeded != 1 || resp.Failed != 2 {
		t.Errorf("counts = %+v", resp)
	}
	if len(resp.Failures) != 2 {
		t.Errorf("failures = %+v", resp.Failures)
	}
}

func TestBatchUpdateTagsValidatesMetadata(t *testing.T) {
	tenant := uuid.New()
	e := &BatchUpdateTagsExecutor{Objects: &tagRepo{updErrs: map[uuid.UUID]error{}}}

	t.Run("tenant mismatch", func(t *testing.T) {
		op := mkOp(t, tenant, batch.BatchUpdateTagsArgs{
			TenantID: uuid.New(), Collection: "k", ObjectIDs: []uuid.UUID{uuid.New()},
		})
		if _, err := e.Execute(context.Background(), op); err == nil {
			t.Error("want a tenant-mismatch error")
		}
	})
	t.Run("missing dependency", func(t *testing.T) {
		op := mkOp(t, tenant, batch.BatchUpdateTagsArgs{
			TenantID: tenant, Collection: "k", ObjectIDs: []uuid.UUID{uuid.New()},
		})
		if _, err := (&BatchUpdateTagsExecutor{}).Execute(context.Background(), op); err == nil {
			t.Error("want a wiring error")
		}
	})
}

// ─── permanent delete ──────────────────────────────────────────────────────

// BatchDelete used to refuse permanent=true at the shim, because the executor
// could only soft-delete and answering "erased" over a soft delete is the one
// outcome worse than refusing. It hard-deletes now, through the SAME
// object.Handler.PermanentDelete the DeleteObject RPC calls, so what these
// tests pin is the routing and the failure contract — the ordering, the purge
// debt and the lock rules are the shared method's own and are tested there.

type fakePermanentDeleter struct {
	calls  []uuid.UUID
	rvs    map[uuid.UUID]int64
	bypass []bool
	errs   map[uuid.UUID]error
}

func newFakePermanentDeleter() *fakePermanentDeleter {
	return &fakePermanentDeleter{rvs: map[uuid.UUID]int64{}, errs: map[uuid.UUID]error{}}
}

func (f *fakePermanentDeleter) PermanentDelete(_ context.Context, _ uuid.UUID, obj object.Object, rv int64, bypass bool) error {
	f.calls = append(f.calls, obj.ObjectID)
	f.rvs[obj.ObjectID] = rv
	f.bypass = append(f.bypass, bypass)
	return f.errs[obj.ObjectID]
}

func TestBatchDeletePermanentHardDeletesEachObject(t *testing.T) {
	tenant := uuid.New()
	id1, id2 := uuid.New(), uuid.New()
	repo := &lookupRepo{objs: []object.Object{
		{ObjectID: id1, ResourceVersion: 3},
		{ObjectID: id2, ResourceVersion: 7},
	}}
	tr := newFakeTransitions()
	pd := newFakePermanentDeleter()
	e := &BatchDeleteExecutor{Objects: repo, Transitions: tr, Permanent: pd}

	body, err := e.Execute(context.Background(), mkOp(t, tenant, batch.BatchDeleteArgs{
		TenantID: tenant, Collection: "k", ObjectIDs: []uuid.UUID{id1, id2}, Permanent: true,
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if resp := decodeDeleteResp(t, body); resp.Succeeded != 2 || resp.Failed != 0 {
		t.Errorf("counts = %+v", resp)
	}
	if len(pd.calls) != 2 {
		t.Fatalf("hard-deleted %d objects, want 2", len(pd.calls))
	}
	if len(tr.softArgs) != 0 {
		t.Error("soft-deleted something on a permanent batch — the caller would be told its erasure succeeded while the objects sit in the trash")
	}
	// The same fresh resource_version the soft path uses, so a concurrent
	// update surfaces as a mismatch rather than being overwritten.
	if pd.rvs[id1] != 3 || pd.rvs[id2] != 7 {
		t.Errorf("resource versions = %v", pd.rvs)
	}
	for _, b := range pd.bypass {
		if b {
			t.Error("bypassed governance retention — the worker has no principal to check the role against")
		}
	}
}

func TestBatchDeletePermanentReportsALockedObjectAndKeepsGoing(t *testing.T) {
	// A compliance-locked object is exactly the per-object failure the batch
	// contract exists for: it must not sink the other 9,999.
	tenant := uuid.New()
	locked, free := uuid.New(), uuid.New()
	repo := &lookupRepo{objs: []object.Object{
		{ObjectID: locked, ResourceVersion: 1},
		{ObjectID: free, ResourceVersion: 1},
	}}
	pd := newFakePermanentDeleter()
	pd.errs[locked] = errors.New("cannot delete: compliance retention until 2030-01-01")
	e := &BatchDeleteExecutor{Objects: repo, Transitions: newFakeTransitions(), Permanent: pd}

	body, err := e.Execute(context.Background(), mkOp(t, tenant, batch.BatchDeleteArgs{
		TenantID: tenant, Collection: "k", ObjectIDs: []uuid.UUID{locked, free}, Permanent: true,
	}))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	resp := decodeDeleteResp(t, body)
	if resp.Succeeded != 1 || resp.Failed != 1 {
		t.Fatalf("counts = %+v, want one of each", resp)
	}
	if len(resp.Failures) != 1 || resp.Failures[0].ObjectID != locked.String() {
		t.Fatalf("failures = %+v, want the locked object named", resp.Failures)
	}
	if !strings.Contains(resp.Failures[0].Reason, "compliance retention") {
		t.Errorf("reason = %q, want the lock's own explanation", resp.Failures[0].Reason)
	}
}

func TestBatchDeletePermanentFailsTheBatchWithNoDeleterWired(t *testing.T) {
	// Falling back to a soft delete here is the exact lie the Unimplemented
	// guard used to prevent. A misconfigured worker must fail loudly.
	tenant := uuid.New()
	id := uuid.New()
	repo := &lookupRepo{objs: []object.Object{{ObjectID: id, ResourceVersion: 1}}}
	tr := newFakeTransitions()
	e := &BatchDeleteExecutor{Objects: repo, Transitions: tr}

	_, err := e.Execute(context.Background(), mkOp(t, tenant, batch.BatchDeleteArgs{
		TenantID: tenant, Collection: "k", ObjectIDs: []uuid.UUID{id}, Permanent: true,
	}))
	if err == nil {
		t.Fatal("a permanent batch with no deleter wired was accepted")
	}
	if len(tr.softArgs) != 0 {
		t.Error("soft-deleted instead of failing")
	}
}

func TestBatchDeleteDefaultsToSoftWhenPermanentIsUnset(t *testing.T) {
	tenant := uuid.New()
	id := uuid.New()
	repo := &lookupRepo{objs: []object.Object{{ObjectID: id, ResourceVersion: 2}}}
	tr := newFakeTransitions()
	pd := newFakePermanentDeleter()
	e := &BatchDeleteExecutor{Objects: repo, Transitions: tr, Permanent: pd}

	if _, err := e.Execute(context.Background(), mkOp(t, tenant, batch.BatchDeleteArgs{
		TenantID: tenant, Collection: "k", ObjectIDs: []uuid.UUID{id},
	})); err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if len(pd.calls) != 0 {
		t.Error("hard-deleted without being asked to")
	}
	if tr.softArgs[id] != 2 {
		t.Errorf("soft delete got rv %d, want 2", tr.softArgs[id])
	}
}
