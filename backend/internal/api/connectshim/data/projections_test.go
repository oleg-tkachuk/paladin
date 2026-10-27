package data

import (
	"context"
	"testing"
	"time"
	"uuid"

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
	listed      operationh.Facets
	counted     *operationh.ListInput
}

func (r *recordingOperations) ListOperations(_ context.Context, in operationh.ListInput) ([]operationh.Operation, string, error) {
	r.newestFirst = &in.NewestFirst
	r.listed = in.Facets
	return nil, "", nil
}

func (r *recordingOperations) CountOperationFacets(_ context.Context, in operationh.ListInput) ([]operationh.FacetCount, error) {
	r.counted = &in
	return []operationh.FacetCount{{Facet: operationh.FacetState, Value: string(operationh.StateFailed), Count: 1}}, nil
}

// The facets narrow the listing, and are counted only when asked and only
// with the first page: a later page is the same listing.
func TestListOperationsFacetCounts(t *testing.T) {
	failed := commonpb.OperationState_OPERATION_STATE_FAILED
	facets := &commonpb.OperationFacets{States: []commonpb.OperationState{failed}}
	for _, tc := range []struct {
		name    string
		count   bool
		token   string
		counted bool
	}{
		{"asked, first page", true, "", true},
		{"asked, a later page", true, uuid.New().String(), false},
		{"not asked", false, "", false},
	} {
		h := &recordingOperations{}
		out, err := (&OperationServer{H: h}).ListOperations(context.Background(), &pb.ListOperationsRequest{
			Facets: facets, CountFacets: tc.count, Page: &commonpb.PageRequest{PageToken: tc.token},
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(h.listed.States) != 1 || h.listed.States[0] != operationh.StateFailed {
			t.Errorf("%s: listed with facets %+v", tc.name, h.listed)
		}
		if (h.counted != nil) != tc.counted || (len(out.GetFacetCounts()) != 0) != tc.counted {
			t.Errorf("%s: counted %t with %d counts, want %t", tc.name, h.counted != nil, len(out.GetFacetCounts()), tc.counted)
		}
		if tc.counted && (len(h.counted.Facets.States) != 1 || out.GetFacetCounts()[0].GetValue() != failed.String()) {
			t.Errorf("%s: counted %+v, answered %v", tc.name, h.counted.Facets, out.GetFacetCounts())
		}
	}
}

func TestListOperationsSortOrder(t *testing.T) {
	for order, newest := range map[commonpb.SortOrder]bool{
		commonpb.SortOrder_SORT_ORDER_DESC:        true,
		commonpb.SortOrder_SORT_ORDER_ASC:         false,
		commonpb.SortOrder_SORT_ORDER_UNSPECIFIED: false,
	} {
		h := &recordingOperations{}
		if _, err := (&OperationServer{H: h}).ListOperations(context.Background(),
			&pb.ListOperationsRequest{SortOrder: order}); err != nil {
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
		State: operationh.StateRunning, Progress: []byte(operationJSON),
	})
	if running.GetMetadata() == nil {
		t.Error("progress dropped from metadata")
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
		if _, err := (&ObjectTagServer{H: h}).DeleteObjectTags(ctx, &pb.DeleteObjectTagsRequest{
			Name: name, ResourceVersion: "1", Keys: keys,
		}); err != nil {
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
			ListDistinctTags(ctx, &pb.ListDistinctTagsRequest{Parent: parent})
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	if got := list("k2").GetPage().GetNextPageToken(); got != "k2" {
		t.Errorf("next page token = %q, want k2", got)
	}
	if list("").GetPage() != nil {
		t.Error("the last page names a next one")
	}
}

// As on the admin plane: a batch that left objects behind says how many.
func TestDataOperationToProtoCarriesTheFailedCount(t *testing.T) {
	got := dataOperationToProto(&operationh.Operation{State: operationh.StateSucceeded, FailedCount: 3})
	if got.GetFailedCount() != 3 {
		t.Errorf("failed_count = %d, want 3", got.GetFailedCount())
	}
}
