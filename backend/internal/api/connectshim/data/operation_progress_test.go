package data

import (
	"testing"

	"google.golang.org/protobuf/types/known/structpb"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/operationh"
)

// The data plane's Operation.metadata is the operation's progress (AIP-151);
// the executor's arguments are the worker's input and stay out of it.
func TestDataOperationMetadataIsProgress(t *testing.T) {
	const (
		argsJSON     = `{"object_ids":["x"]}`
		progressJSON = `{"processed":3,"total":9}`
	)

	reported := dataOperationToProto(&operationh.Operation{
		State: operationh.StateRunning, Metadata: []byte(argsJSON), Progress: []byte(progressJSON),
	})
	var st structpb.Struct
	if err := reported.GetMetadata().UnmarshalTo(&st); err != nil {
		t.Fatalf("unpack metadata: %v", err)
	}
	if got := st.AsMap(); got["processed"] != float64(3) || got["total"] != float64(9) || got["object_ids"] != nil {
		t.Errorf("metadata = %v, want the progress snapshot alone", got)
	}

	unreported := dataOperationToProto(&operationh.Operation{
		State: operationh.StateRunning, Metadata: []byte(argsJSON),
	})
	if unreported.GetMetadata() != nil {
		t.Errorf("metadata = %v, want none before the first progress report", unreported.GetMetadata())
	}
}
