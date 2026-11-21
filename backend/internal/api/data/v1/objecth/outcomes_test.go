package objecth

import (
	"context"
	"errors"
	"testing"
	"time"
	"uuid"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/statemachine"
)

// objectRPC is one RPC of the package's handlers, run on an unauthenticated
// context with every handler logging to l.
type objectRPC struct {
	op   string
	call func(ctx context.Context, l *zap.Logger)
}

func objectRPCs() []objectRPC {
	h := func(l *zap.Logger) *Handler { x := &Handler{}; x.SetLogger(l); return x }
	lh := func(l *zap.Logger) *LockHandler { x := &LockHandler{}; x.SetLogger(l); return x }
	th := func(l *zap.Logger) *TaintHandler { x := &TaintHandler{}; x.SetLogger(l); return x }
	vh := func(l *zap.Logger) *VersionHandler { x := &VersionHandler{}; x.SetLogger(l); return x }
	future := time.Now().Add(time.Hour)
	return []objectRPC{
		{opUpload, func(ctx context.Context, l *zap.Logger) {
			_, _ = h(l).UploadObject(ctx, UploadObjectInput{Collection: "docs"})
		}},
		{opComplete, func(ctx context.Context, l *zap.Logger) {
			_, _ = h(l).CompleteObject(ctx, CompleteObjectInput{Collection: "docs", ObjectID: "o"})
		}},
		{opListFolders, func(ctx context.Context, l *zap.Logger) {
			_, _, _ = h(l).ListCommonPrefixes(ctx, ListObjectsInput{Collection: "docs", Delimiter: "/"})
		}},
		{opList, func(ctx context.Context, l *zap.Logger) {
			_, _, _ = h(l).ListObjects(ctx, ListObjectsInput{Collection: "docs"})
		}},
		{opCount, func(ctx context.Context, l *zap.Logger) {
			_, _ = h(l).CountObjects(ctx, CountObjectsInput{Collection: "docs"})
		}},
		{opCountFacets, func(ctx context.Context, l *zap.Logger) {
			_, _ = h(l).CountObjectFacets(ctx, ListObjectsInput{Collection: "docs"})
		}},
		{opListTags, func(ctx context.Context, l *zap.Logger) { _, _ = h(l).ListDistinctTags(ctx, "docs", "", 0) }},
		{opGet, func(ctx context.Context, l *zap.Logger) { _, _ = h(l).GetObject(ctx, "docs", "o") }},
		{opLookup, func(ctx context.Context, l *zap.Logger) { _, _ = h(l).LookupObject(ctx, "docs", "k") }},
		{opDownload, func(ctx context.Context, l *zap.Logger) { _, _ = h(l).DownloadObject(ctx, "docs", "o", 0, "", false) }},
		{opUpdate, func(ctx context.Context, l *zap.Logger) {
			_, _ = h(l).UpdateObject(ctx, UpdateObjectInput{Collection: "docs", ObjectID: "o"})
		}},
		{opDelete, func(ctx context.Context, l *zap.Logger) { _ = h(l).DeleteObject(ctx, "docs", "o", "", false, false) }},
		{opRestore, func(ctx context.Context, l *zap.Logger) { _, _ = h(l).RestoreObject(ctx, "docs", "o", "1") }},
		{opCopy, func(ctx context.Context, l *zap.Logger) {
			_, _ = h(l).CopyObject(ctx, CopyObjectInput{SourceCollection: "docs", SourceObjectID: "o", DestCollection: "b"})
		}},
		{opSetTaint, func(ctx context.Context, l *zap.Logger) { _, _ = th(l).SetTaint(ctx, "docs", "o", nil) }},
		{opSetRetention, func(ctx context.Context, l *zap.Logger) {
			_, _ = lh(l).SetRetention(ctx, SetRetentionInput{Collection: "docs", ObjectID: "o", Mode: LockModeGovernance, RetainUntil: future})
		}},
		{opSetLegalHold, func(ctx context.Context, l *zap.Logger) { _, _ = lh(l).SetLegalHold(ctx, "docs", "o", true) }},
		{opGetLock, func(ctx context.Context, l *zap.Logger) { _, _ = lh(l).GetLock(ctx, "docs", "o") }},
		{opListVersions, func(ctx context.Context, l *zap.Logger) {
			_, _, _ = vh(l).ListVersions(ctx, ListVersionsInput{Collection: "docs", ObjectID: "o"})
		}},
		{opGetVersion, func(ctx context.Context, l *zap.Logger) { _, _ = vh(l).GetVersion(ctx, "v") }},
		{opRestoreVersion, func(ctx context.Context, l *zap.Logger) { _, _ = vh(l).RestoreVersion(ctx, "v", "1") }},
	}
}

// Every RPC says how it ended. Refused for want of a caller, each says so
// once, at warn, naming itself.
func TestEveryObjectRPCLogsItsOutcome(t *testing.T) {
	for _, rpc := range objectRPCs() {
		t.Run(rpc.op, func(t *testing.T) {
			core, logs := observer.New(zapcore.DebugLevel)
			rpc.call(context.Background(), zap.New(core))
			lines := logs.All()
			if len(lines) != 1 || lines[0].Level != zapcore.WarnLevel || lines[0].ContextMap()["op"] != rpc.op {
				t.Fatalf("want one warn line naming %q, got %v", rpc.op, lines)
			}
		})
	}
}

// findRepo answers FindByName with obj, or err.
type findRepo struct {
	fakeObjectRepo
	obj Object
	err error
}

func (r *findRepo) FindByName(context.Context, uuid.UUID, string, string) (Object, error) {
	return r.obj, r.err
}

// An outcome is logged at the level it deserves — a read done at debug, a
// refusal at warn, a store failure at error — with the tenant, the
// collection and the object it was about.
func TestObjectOutcomeLevels(t *testing.T) {
	tid, oid := uuid.New(), uuid.New()
	available := Object{ObjectID: oid, Collection: "docs", Key: "a.txt", State: statemachine.StateAvailable}
	for name, tc := range map[string]struct {
		repo  *findRepo
		level zapcore.Level
	}{
		"read":    {&findRepo{obj: available}, zapcore.DebugLevel},
		"refused": {&findRepo{err: ErrObjectNotFound}, zapcore.WarnLevel},
		"failed":  {&findRepo{err: errors.New("connection refused")}, zapcore.ErrorLevel},
	} {
		t.Run(name, func(t *testing.T) {
			core, logs := observer.New(zapcore.DebugLevel)
			h := &Handler{repo: tc.repo, policy: allowAll{}}
			h.SetLogger(zap.New(core))
			_, _ = h.GetObject(purgeCtx(tid), "docs", oid.String())
			lines := logs.FilterLevelExact(tc.level).All()
			if len(lines) != 1 || len(logs.All()) != 1 {
				t.Fatalf("want one %v line, got %v", tc.level, logs.All())
			}
			fields := lines[0].ContextMap()
			if fields["op"] != opGet || fields["tenant_id"] != tid.String() ||
				fields["collection"] != "docs" || fields["object_id"] != oid.String() {
				t.Errorf("line lacks op, tenant, collection or object: %v", fields)
			}
		})
	}
}

// A change is logged at info.
func TestObjectChangeIsLoggedAtInfo(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	repo := &stateUpdateRepo{cur: Object{ObjectID: uuid.New(), Collection: "docs", State: statemachine.StateAvailable, ResourceVersion: 1}}
	h := &Handler{repo: repo, policy: allowAll{}}
	h.SetLogger(zap.New(core))
	if _, err := h.UpdateObject(purgeCtx(uuid.New()), UpdateObjectInput{
		Collection: "docs", ObjectID: repo.cur.ObjectID.String(), ResourceVersion: 1, UpdatedFields: []string{FieldTags},
	}); err != nil {
		t.Fatal(err)
	}
	if lines := logs.FilterLevelExact(zapcore.InfoLevel).All(); len(lines) != 1 || lines[0].ContextMap()["op"] != opUpdate {
		t.Fatalf("want one info line for the update, got %v", logs.All())
	}
}

// The wiring builds no lock, taint or version handler where their repository
// is not wired, and still hands each a logger: a nil handler takes it.
func TestSetLoggerOnAnAbsentHandler(t *testing.T) {
	var (
		lh *LockHandler
		th *TaintHandler
		vh *VersionHandler
	)
	lh.SetLogger(zap.NewNop())
	th.SetLogger(zap.NewNop())
	vh.SetLogger(zap.NewNop())
}

// SetLogger(nil) keeps the handler logging nowhere rather than panicking.
func TestNilLoggerLogsNowhere(t *testing.T) {
	h := &Handler{}
	h.SetLogger(nil)
	if _, err := h.GetObject(context.Background(), "docs", "o"); err == nil {
		t.Fatal("an unauthenticated read succeeded")
	}
}
