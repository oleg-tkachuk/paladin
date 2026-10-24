package admin

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"
	rpccode "google.golang.org/genproto/googleapis/rpc/code"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/batchh"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/operationh"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	commonpb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
)

// What the platform-operation shim builds from the handler's answer: each
// case fails if its condition in the shim is inverted or weakened.

const (
	operationPayloadJSON = `{"k":"v"}`
	operationArgsJSON    = `{"object_ids":["x"]}`
)

type recordingOperation struct {
	failingOperation
	newestFirst *bool
	mine        bool
	tenant      uuid.UUID
}

func (r *recordingOperation) ListOperations(_ context.Context, in operationh.ListInput) ([]operationh.Operation, string, error) {
	r.newestFirst = &in.NewestFirst
	r.mine = in.Mine
	r.tenant = in.TenantID
	return nil, "", nil
}

// tenant_id reaches the handler as the tenant to list, and none as the
// caller's own; one that is no id is refused before it.
func TestListPlatformOperationsOfANamedTenant(t *testing.T) {
	named := uuid.New()
	for id, want := range map[string]uuid.UUID{named.String(): named, "": uuid.Nil} {
		h := &recordingOperation{}
		if _, err := (&OperationServer{H: h}).ListOperations(context.Background(), &pb.ListOperationsRequest{TenantId: id}); err != nil {
			t.Fatal(err)
		}
		if h.tenant != want {
			t.Errorf("tenant_id %q reached the handler as %v, want %v", id, h.tenant, want)
		}
	}
	h := &recordingOperation{}
	_, err := (&OperationServer{H: h}).ListOperations(context.Background(), &pb.ListOperationsRequest{TenantId: "not-an-id"})
	if connect.CodeOf(err) != connect.CodeInvalidArgument || h.newestFirst != nil {
		t.Errorf("err = %v, handler reached = %t; want InvalidArgument before it", err, h.newestFirst != nil)
	}
}

// mine reaches the handler, and the initiator reaches the caller.
func TestListPlatformOperations_MineAndInitiator(t *testing.T) {
	h := &recordingOperation{}
	if _, err := (&OperationServer{H: h}).ListOperations(context.Background(), &pb.ListOperationsRequest{Mine: true}); err != nil {
		t.Fatal(err)
	}
	if !h.mine {
		t.Error("mine did not reach the handler")
	}
	got := operationToProto(&operationh.Operation{OperationID: uuid.New(), InitiatorSubject: "0199-user"})
	if got.GetInitiatorSubject() != "0199-user" {
		t.Errorf("initiator_subject = %q", got.GetInitiatorSubject())
	}
}

func TestListPlatformOperationsSortOrder(t *testing.T) {
	for order, newest := range map[commonpb.SortOrder]bool{
		commonpb.SortOrder_SORT_ORDER_DESC:        true,
		commonpb.SortOrder_SORT_ORDER_ASC:         false,
		commonpb.SortOrder_SORT_ORDER_UNSPECIFIED: false,
	} {
		h := &recordingOperation{}
		if _, err := (&OperationServer{H: h}).ListOperations(context.Background(),
			&pb.ListOperationsRequest{SortOrder: order}); err != nil {
			t.Fatal(err)
		}
		if h.newestFirst == nil || *h.newestFirst != newest {
			t.Errorf("%v: newestFirst = %v, want %v", order, h.newestFirst, newest)
		}
	}
}

func TestOperationID(t *testing.T) {
	id := uuid.New()
	cases := map[string]bool{
		"operations/" + id.String(): true,
		// As long as "operations/" but another word: the prefix, not the
		// length, refuses it.
		"operationz/" + id.String(): false,
		"operations/not-a-uuid":     false,
		// The length check is not what refuses this one: uuid.Parse("") does
		// too, so weakening `<=` to `<` changes nothing a caller sees.
		"operations/": false,
	}
	for name, ok := range cases {
		got, err := operationID(name)
		if ok && (err != nil || got != id) {
			t.Errorf("%q: got %v, %v", name, got, err)
		}
		if !ok && err == nil {
			t.Errorf("%q: accepted", name)
		}
	}
}

func TestOperationToProto(t *testing.T) {
	if operationToProto(nil) != nil {
		t.Error("no operation, yet a message")
	}

	running := operationToProto(&operationh.Operation{
		State: operationh.StateRunning, Metadata: []byte(operationArgsJSON), Progress: []byte(operationPayloadJSON),
	})
	if got := structOf(t, running.GetMetadata()); got["k"] != "v" {
		t.Errorf("metadata = %v, want the progress snapshot", got)
	}
	// The executor's arguments are the worker's input; the API's metadata is
	// progress (AIP-151), and an operation that has not reported has none.
	unreported := operationToProto(&operationh.Operation{
		State: operationh.StateRunning, Metadata: []byte(operationArgsJSON),
	})
	if unreported.GetMetadata() != nil {
		t.Errorf("metadata = %v, want none before the first progress report", unreported.GetMetadata())
	}
	if running.GetDone() || running.GetResult() != nil {
		t.Errorf("a running operation reports done=%v result=%v", running.GetDone(), running.GetResult())
	}

	succeeded := operationToProto(&operationh.Operation{
		State: operationh.StateSucceeded, Response: []byte(operationPayloadJSON),
	})
	if succeeded.GetResponse() == nil || !succeeded.GetDone() {
		t.Errorf("a succeeded operation lost its response: %+v", succeeded)
	}

	for state, cancelled := range map[operationh.State]bool{
		operationh.StateFailed:    false,
		operationh.StateCancelled: true,
	} {
		op := operationToProto(&operationh.Operation{State: state, ErrorCode: "boom"})
		if !op.GetDone() {
			t.Errorf("%s: not done", state)
		}
		if got := rpccode.Code(op.GetError().GetCode()) == rpccode.Code_CANCELLED; got != cancelled {
			t.Errorf("%s: reads as cancelled = %v", state, got)
		}
	}
}

func structOf(t *testing.T, a *anypb.Any) map[string]any {
	t.Helper()
	if a == nil {
		return nil
	}
	var st structpb.Struct
	if err := a.UnmarshalTo(&st); err != nil {
		t.Fatalf("unpack metadata: %v", err)
	}
	return st.AsMap()
}

// A listed operation says where it stands, when it finished, and what it
// acted on — the drawer's whole account of it.
func TestOperationProjection_StateTargetAndFinish(t *testing.T) {
	tenant := uuid.New()
	done := time.Date(2026, 10, 18, 12, 0, 0, 0, time.UTC)
	md, err := json.Marshal(batchh.BatchDeleteArgs{
		TenantID: tenant, Collection: "docs", ObjectIDs: []uuid.UUID{uuid.New()}, Permanent: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := operationToProto(&operationh.Operation{
		OperationID: uuid.New(), TenantID: tenant, Type: batchh.OpBatchDelete,
		State: operationh.StateCancelled, Metadata: md, DoneAt: &done,
	})
	if got.GetState() != commonpb.OperationState_OPERATION_STATE_CANCELLED {
		t.Errorf("state = %v", got.GetState())
	}
	if !got.GetDoneAt().AsTime().Equal(done) {
		t.Errorf("done_at = %v, want %v", got.GetDoneAt().AsTime(), done)
	}
	tg := got.GetTarget()
	if tg.GetCollection() != batchh.CollectionName(tenant, "docs") || tg.GetObjectCount() != 1 || !tg.GetPermanent() {
		t.Errorf("target = %v", tg)
	}

	running := operationToProto(&operationh.Operation{OperationID: uuid.New(), Type: "Reindex", State: operationh.StateRunning})
	if running.GetDoneAt() != nil || running.GetTarget() != nil {
		t.Errorf("a running operation of an unknown type: done_at %v, target %v", running.GetDoneAt(), running.GetTarget())
	}
}

// A batch that left objects behind says how many, so a client tells it from a
// clean one without decoding its response.
func TestOperationToProtoCarriesTheFailedCount(t *testing.T) {
	got := operationToProto(&operationh.Operation{OperationID: uuid.New(), State: operationh.StateSucceeded, FailedCount: 3})
	if got.GetFailedCount() != 3 {
		t.Errorf("failed_count = %d, want 3", got.GetFailedCount())
	}
}
