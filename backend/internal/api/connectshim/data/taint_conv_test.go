package data

import (
	"context"
	"slices"
	"testing"

	"connectrpc.com/connect"

	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
)

func TestTaintRoundTripsBetweenStoredNamesAndTheEnum(t *testing.T) {
	stored := []string{objecth.TaintPII, objecth.TaintPromptInjection, objecth.TaintSecrets}
	if back := taintFromProto(taintToProto(stored)); !slices.Equal(back, stored) {
		t.Fatalf("round trip = %v, want %v", back, stored)
	}
	// An enum value with no stored name is passed through by its proto
	// name so the handler rejects it, rather than dropped.
	if got := taintFromProto([]pb.TaintSignal{pb.TaintSignal_TAINT_SIGNAL_UNSPECIFIED}); len(got) != 1 || got[0] != "TAINT_SIGNAL_UNSPECIFIED" {
		t.Fatalf("unspecified = %v", got)
	}
	if got := objectToProto(&objecth.Object{Taint: []string{objecth.TaintSecrets}}).GetTaint(); len(got) != 1 || got[0] != pb.TaintSignal_TAINT_SIGNAL_SECRETS {
		t.Fatalf("objectToProto taint = %v", got)
	}
}

func TestSetObjectTaintUnimplementedWhenNotWired(t *testing.T) {
	s := &ObjectServer{}
	_, err := s.SetObjectTaint(context.Background(), connect.NewRequest(&pb.SetObjectTaintRequest{Name: "x"}))
	if connect.CodeOf(err) != connect.CodeUnimplemented {
		t.Fatalf("err = %v, want Unimplemented", err)
	}
	if s2 := (&ObjectServer{}).WithTaints(nil); s2.Taints != nil {
		t.Fatal("a nil *TaintHandler became a non-nil interface")
	}
}
