package data

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	rpccode "google.golang.org/genproto/googleapis/rpc/code"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/operationh"
	commonpb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// What the shim builds from a handler's answer, field by field: each case
// fails if its condition in the shim is inverted or weakened.

const (
	governanceMode = "GOVERNANCE"
	operationJSON  = `{"k":"v"}`
)

// ─── operations ────────────────────────────────────────────────────────────

type recordingOperations struct {
	failingOperations
	newestFirst *bool
}

func (r *recordingOperations) ListOperations(_ context.Context, _ *operationh.State, _ int32, _, _ string, newestFirst bool) ([]operationh.Operation, string, error) {
	r.newestFirst = &newestFirst
	return nil, "", nil
}

func TestListOperationsSortOrder(t *testing.T) {
	for order, newest := range map[commonpb.SortOrder]bool{
		commonpb.SortOrder_SORT_ORDER_DESC:        true,
		commonpb.SortOrder_SORT_ORDER_ASC:         false,
		commonpb.SortOrder_SORT_ORDER_UNSPECIFIED: false,
	} {
		h := &recordingOperations{}
		if _, err := (&OperationServer{H: h}).ListOperations(context.Background(),
			connect.NewRequest(&pb.ListOperationsRequest{SortOrder: order})); err != nil {
			t.Fatal(err)
		}
		if h.newestFirst == nil || *h.newestFirst != newest {
			t.Errorf("%v: newestFirst = %v, want %v", order, h.newestFirst, newest)
		}
	}
}

func TestDataOperationID(t *testing.T) {
	id := uuid.New()
	cases := map[string]bool{
		"operations/" + id.String(): true,
		// As long as "operations/" but another word: the prefix, not the
		// length, refuses it.
		"operationz/" + id.String(): false,
		"operations/not-a-uuid":     false,
		id.String():                 false,
		// The length check is not what refuses this one: uuid.Parse("") does
		// too, so weakening `<=` to `<` changes nothing a caller sees.
		"operations/": false,
	}
	for name, ok := range cases {
		got, err := dataOperationID(name)
		if ok && (err != nil || got != id) {
			t.Errorf("%q: got %v, %v", name, got, err)
		}
		if !ok && err == nil {
			t.Errorf("%q: accepted", name)
		}
	}
}

func TestDataOperationToProtoCarriesItsPayloads(t *testing.T) {
	running := dataOperationToProto(&operationh.Operation{
		State: operationh.StateRunning, Metadata: []byte(operationJSON),
	})
	if running.GetMetadata() == nil {
		t.Error("metadata dropped")
	}
	if running.GetDone() || running.GetResult() != nil {
		t.Errorf("a running operation reports done=%v result=%v", running.GetDone(), running.GetResult())
	}

	succeeded := dataOperationToProto(&operationh.Operation{
		State: operationh.StateSucceeded, Response: []byte(operationJSON),
	})
	if succeeded.GetResponse() == nil || !succeeded.GetDone() {
		t.Errorf("a succeeded operation lost its response: %+v", succeeded)
	}

	failed := dataOperationToProto(&operationh.Operation{State: operationh.StateFailed, ErrorCode: "boom"})
	cancelled := dataOperationToProto(&operationh.Operation{State: operationh.StateCancelled, ErrorCode: "boom"})
	if got := rpccode.Code(failed.GetError().GetCode()); got == rpccode.Code_CANCELLED {
		t.Errorf("a failed operation reads as cancelled")
	}
	if got := rpccode.Code(cancelled.GetError().GetCode()); got != rpccode.Code_CANCELLED {
		t.Errorf("a cancelled operation has code %v", got)
	}
}

// ─── locks and checksums ───────────────────────────────────────────────────

// Each lock field alone is a lock; none is not reported.
func TestObjectToProtoReportsAnyLockField(t *testing.T) {
	until := time.Now()
	for name, lock := range map[string]objecth.ObjectLock{
		"mode":         {Mode: governanceMode},
		"legal hold":   {LegalHold: true},
		"retain until": {RetainUntil: &until},
	} {
		if objectToProto(&objecth.Object{Lock: lock}).GetLock() == nil {
			t.Errorf("%s alone: lock not reported", name)
		}
	}
	if objectToProto(&objecth.Object{}).GetLock() != nil {
		t.Error("an unlocked object reports a lock")
	}
}

func TestVersionToProtoReportsAnyLockOrChecksumField(t *testing.T) {
	until := time.Now()
	for name, v := range map[string]objecth.ObjectVersion{
		"mode":         {LockMode: governanceMode},
		"retain until": {LockRetainUntil: &until},
	} {
		if versionToProto(paladin.ObjectName{}, &v).GetLock() == nil {
			t.Errorf("%s alone: lock not reported", name)
		}
	}
	if versionToProto(paladin.ObjectName{}, &objecth.ObjectVersion{Checksum: "abc"}).GetChecksum() == nil {
		t.Error("a checksum without an algorithm is dropped")
	}
}

// ─── tags ──────────────────────────────────────────────────────────────────

type recordingTags struct {
	tags    map[string]string
	written map[string]string
	page    objecth.DistinctTagPage
}

func (r *recordingTags) GetObject(context.Context, string, string) (*objecth.Object, error) {
	return &objecth.Object{Tags: r.tags}, nil
}

func (r *recordingTags) UpdateObject(_ context.Context, in objecth.UpdateObjectInput) (*objecth.Object, error) {
	r.written = in.Tags
	return &objecth.Object{Tags: in.Tags}, nil
}

func (r *recordingTags) ListDistinctTags(context.Context, string, string, int32) (objecth.DistinctTagPage, error) {
	return r.page, nil
}

func TestDeleteObjectTagsKeysSelectWhatIsDropped(t *testing.T) {
	ctx := ctxTenant(tenantA)
	name := "tenants/" + tenantA.String() + "/collections/c1/objects/" + objUUID.String()
	del := func(keys ...string) map[string]string {
		h := &recordingTags{tags: map[string]string{"a": "1", "b": "2"}}
		if _, err := (&ObjectTagServer{H: h}).DeleteObjectTags(ctx, connect.NewRequest(&pb.DeleteObjectTagsRequest{
			Name: name, ResourceVersion: "1", Keys: keys,
		})); err != nil {
			t.Fatal(err)
		}
		return h.written
	}
	if got := del(); len(got) != 0 {
		t.Errorf("no keys: kept %v, want every tag dropped", got)
	}
	if got := del("a"); len(got) != 1 || got["b"] != "2" {
		t.Errorf("key a: kept %v, want only b", got)
	}
}

func TestListDistinctTagsNextPage(t *testing.T) {
	ctx := ctxTenant(tenantA)
	parent := "tenants/" + tenantA.String() + "/collections/c1"
	list := func(next string) *pb.ListDistinctTagsResponse {
		resp, err := (&ObjectTagServer{H: &recordingTags{page: objecth.DistinctTagPage{NextKey: next}}}).
			ListDistinctTags(ctx, connect.NewRequest(&pb.ListDistinctTagsRequest{Parent: parent}))
		if err != nil {
			t.Fatal(err)
		}
		return resp.Msg
	}
	if got := list("k2").GetPage().GetNextPageToken(); got != "k2" {
		t.Errorf("next page token = %q, want k2", got)
	}
	if list("").GetPage() != nil {
		t.Error("the last page names a next one")
	}
}
