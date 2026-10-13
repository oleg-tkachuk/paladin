package admin

import (
	"context"
	"testing"

	"github.com/google/uuid"
	rpccode "google.golang.org/genproto/googleapis/rpc/code"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"

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
}

func (r *recordingOperation) ListOperations(_ context.Context, _ *operationh.State, _ int32, _, _ string, newestFirst bool) ([]operationh.Operation, string, error) {
	r.newestFirst = &newestFirst
	return nil, "", nil
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
