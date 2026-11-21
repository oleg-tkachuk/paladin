package batchh

import (
	"context"
	"errors"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/bucketgate"
	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
)

// gatedBuckets resolves every collection through bucketgate.Check against
// state, as the store adapter does; lookupErr, when set, is the lookup itself
// failing instead.
type gatedBuckets struct {
	state     bucketgate.State
	lookupErr error
}

func (g gatedBuckets) LookupBucket(_ context.Context, _ uuid.UUID, _ string, write bool) (string, string, error) {
	if g.lookupErr != nil {
		return "", "", g.lookupErr
	}
	if err := bucketgate.Check(bucketgate.AccessOf(write), g.state); err != nil {
		return "", "", err
	}
	return "backend-def", "bucket-def", nil
}

// The batch RPCs that write, with the Cedar action of the write they submit.
var batchWrites = []struct {
	name   string
	action cedar.Action
	call   func(h *Handler, ctx context.Context) error
}{
	{"BatchDelete", cedar.ActionDeleteObject, func(h *Handler, ctx context.Context) error {
		_, err := h.BatchDelete(ctx, BatchDeleteArgs{Collection: "docs", ObjectIDs: ids(1)})
		return err
	}},
	{"BatchCopy", cedar.ActionPutObject, func(h *Handler, ctx context.Context) error {
		_, err := h.BatchCopy(ctx, BatchCopyArgs{ObjectIDs: ids(1), SrcCollection: "src", DstCollection: "dst"})
		return err
	}},
	{"BatchUpdateTags", cedar.ActionUpdateObject, func(h *Handler, ctx context.Context) error {
		_, err := h.BatchUpdateTags(ctx, BatchUpdateTagsArgs{Collection: "docs", ObjectIDs: ids(1)})
		return err
	}},
	{"BatchRestoreObjects", cedar.ActionRestoreObject, func(h *Handler, ctx context.Context) error {
		_, err := h.BatchRestoreObjects(ctx, BatchRestoreObjectsArgs{Collection: "docs", ObjectIDs: ids(1)})
		return err
	}},
}

// A batch that writes answers the bucket's state as the single-object write
// does, and is never authorized — let alone submitted — on a binding the gate
// refused or could not read. It used to resolve its binding best-effort and
// submit with an empty one.
func TestBatchWritesAnswerTheBucketGate(t *testing.T) {
	states := []struct {
		name    string
		buckets gatedBuckets
		want    connect.Code
	}{
		{"draining", gatedBuckets{state: bucketgate.State{Enabled: true, ReadOnly: true, ProvisionState: bucketgate.ProvisionReady}}, connect.CodeFailedPrecondition},
		{"provisioning", gatedBuckets{state: bucketgate.State{Enabled: true, ProvisionState: bucketgate.ProvisionPending}}, connect.CodeUnavailable},
		{"disabled", gatedBuckets{state: bucketgate.State{ProvisionState: bucketgate.ProvisionReady}}, connect.CodeFailedPrecondition},
		{"store unreachable", gatedBuckets{lookupErr: errors.New("connection reset")}, connect.CodeInternal},
	}
	for _, rpc := range batchWrites {
		for _, st := range states {
			t.Run(rpc.name+"/"+st.name, func(t *testing.T) {
				sub := &fakeSubmitter{id: uuid.New()}
				az := allowAll()
				err := rpc.call(NewHandler(sub, az, st.buckets, &fakeObjects{}), authedCtx(uuid.New()))
				wantCode(t, err, st.want)
				if sub.called {
					t.Fatal("a refused batch was submitted")
				}
				for _, a := range az.actions {
					if a == rpc.action {
						t.Fatalf("Cedar decided %s on a binding the gate refused", a)
					}
				}
			})
		}
	}
}

// A healthy bucket lets every batch write through to its submit.
func TestBatchWritesPassAHealthyBucket(t *testing.T) {
	for _, rpc := range batchWrites {
		t.Run(rpc.name, func(t *testing.T) {
			sub := &fakeSubmitter{id: uuid.New()}
			buckets := gatedBuckets{state: bucketgate.State{Enabled: true, ProvisionState: bucketgate.ProvisionReady}}
			if err := rpc.call(NewHandler(sub, allowAll(), buckets, &fakeObjects{}), authedCtx(uuid.New())); err != nil {
				t.Fatalf("%s: %v", rpc.name, err)
			}
			if !sub.called {
				t.Fatal("a healthy batch was not submitted")
			}
		})
	}
}

// A copy only reads its source: a draining source backend still lets a copy
// out of it, which is how data is moved off a drain.
func TestBatchCopyReadsADrainingSource(t *testing.T) {
	sub := &fakeSubmitter{id: uuid.New()}
	h := NewHandler(sub, allowAll(), perCollection{
		"src": {Enabled: true, ReadOnly: true, ProvisionState: bucketgate.ProvisionReady},
		"dst": {Enabled: true, ProvisionState: bucketgate.ProvisionReady},
	}, &fakeObjects{})
	if _, err := h.BatchCopy(authedCtx(uuid.New()), BatchCopyArgs{ObjectIDs: ids(1), SrcCollection: "src", DstCollection: "dst"}); err != nil {
		t.Fatalf("BatchCopy from a draining source: %v", err)
	}
	if !sub.called {
		t.Fatal("copy off a draining backend was not submitted")
	}
}

// A write with no bucket resolver wired is refused rather than authorized on
// an empty binding.
func TestBatchWriteWithoutAResolverIsRefused(t *testing.T) {
	sub := &fakeSubmitter{id: uuid.New()}
	_, err := NewHandler(sub, allowAll(), nil, &fakeObjects{}).
		BatchDelete(authedCtx(uuid.New()), BatchDeleteArgs{Collection: "docs", ObjectIDs: ids(1)})
	wantCode(t, err, connect.CodeUnavailable)
	if sub.called {
		t.Fatal("a batch with no resolver was submitted")
	}
}

// perCollection gates each collection on its own state.
type perCollection map[string]bucketgate.State

func (p perCollection) LookupBucket(_ context.Context, _ uuid.UUID, collection string, write bool) (string, string, error) {
	if err := bucketgate.Check(bucketgate.AccessOf(write), p[collection]); err != nil {
		return "", "", err
	}
	return "backend-" + collection, "bucket-" + collection, nil
}

// Each way the write gate stops a batch leaves one line saying so: a store
// failure at Error, a refusal at Warn, both naming the collection.
func TestBatchWriteGateIsLogged(t *testing.T) {
	for _, tc := range []struct {
		name    string
		buckets BucketResolver
		level   zapcore.Level
		msg     string
	}{
		{"store unreachable", gatedBuckets{lookupErr: errors.New("connection reset")}, zapcore.ErrorLevel, msgBindingUnread},
		{"draining", gatedBuckets{state: bucketgate.State{Enabled: true, ReadOnly: true, ProvisionState: bucketgate.ProvisionReady}}, zapcore.WarnLevel, msgGateRefused},
		{"no resolver", nil, zapcore.ErrorLevel, msgNoBucketResolver},
	} {
		t.Run(tc.name, func(t *testing.T) {
			core, logs := observer.New(zapcore.DebugLevel)
			h := NewHandler(&fakeSubmitter{id: uuid.New()}, allowAll(), tc.buckets, &fakeObjects{})
			h.SetLogger(zap.New(core))
			if _, err := h.BatchDelete(authedCtx(uuid.New()), BatchDeleteArgs{Collection: "docs", ObjectIDs: ids(1)}); err == nil {
				t.Fatal("a gated batch was accepted")
			}
			lines := logs.FilterMessage(tc.msg).All()
			if len(lines) != 1 || lines[0].Level != tc.level || lines[0].ContextMap()[fieldCollection] != "docs" {
				t.Fatalf("lines %q = %v, want one at %v naming the collection; all %v", tc.msg, lines, tc.level, logs.All())
			}
		})
	}
}

// A source binding that cannot be read is warned about, and the copy is
// still decided on — the source is only read.
func TestBatchCopyWarnsOnAnUnreadableSource(t *testing.T) {
	core, logs := observer.New(zapcore.DebugLevel)
	sub := &fakeSubmitter{id: uuid.New()}
	h := NewHandler(sub, allowAll(), perCollection{
		"dst": {Enabled: true, ProvisionState: bucketgate.ProvisionReady},
	}, &fakeObjects{})
	h.SetLogger(zap.New(core))
	if _, err := h.BatchCopy(authedCtx(uuid.New()), BatchCopyArgs{ObjectIDs: ids(1), SrcCollection: "src", DstCollection: "dst"}); err != nil {
		t.Fatalf("BatchCopy: %v", err)
	}
	if n := logs.FilterMessage(msgSourceUnresolved).Len(); n != 1 {
		t.Fatalf("source warnings = %d, want 1; logs %v", n, logs.All())
	}
}
