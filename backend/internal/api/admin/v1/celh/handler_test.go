package celh

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"

	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

func TestValidate_EmptyExpression(t *testing.T) {
	h := NewHandler()
	resp, err := h.Validate(context.Background(), connect.NewRequest(&pb.ValidateCELRequest{
		Schema:     "Object",
		Expression: "",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Msg.GetValid() {
		t.Fatalf("expected valid=true for empty expression, got %+v", resp.Msg)
	}
}

func TestValidate_ValidExpression(t *testing.T) {
	h := NewHandler()
	resp, err := h.Validate(context.Background(), connect.NewRequest(&pb.ValidateCELRequest{
		Schema:     "EventEnvelope",
		Expression: `type == "paladin.object.uploaded"`,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !resp.Msg.GetValid() {
		t.Fatalf("expected valid=true, got %+v", resp.Msg)
	}
	if resp.Msg.GetMessage() != "" {
		t.Fatalf("expected empty message, got %q", resp.Msg.GetMessage())
	}
}

func TestValidate_InvalidExpression(t *testing.T) {
	h := NewHandler()
	resp, err := h.Validate(context.Background(), connect.NewRequest(&pb.ValidateCELRequest{
		Schema:     "Object",
		Expression: `nonexistent_field == "x"`,
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.Msg.GetValid() {
		t.Fatalf("expected valid=false, got %+v", resp.Msg)
	}
	if resp.Msg.GetMessage() == "" {
		t.Fatal("expected non-empty message")
	}
	if resp.Msg.GetLine() < 1 {
		t.Fatalf("expected Line >= 1, got %d", resp.Msg.GetLine())
	}
}

func TestValidate_UnknownSchema(t *testing.T) {
	h := NewHandler()
	_, err := h.Validate(context.Background(), connect.NewRequest(&pb.ValidateCELRequest{
		Schema:     "BogusSchema",
		Expression: "true",
	}))
	if err == nil {
		t.Fatal("expected error for unknown schema")
	}
	var ce *connect.Error
	if !asConnectError(err, &ce) {
		t.Fatalf("expected connect.Error, got %T", err)
	}
	if ce.Code() != connect.CodeInvalidArgument {
		t.Fatalf("expected CodeInvalidArgument, got %s", ce.Code())
	}
}

// asConnectError unwraps to *connect.Error without bringing in errors.As
// (keeps the test file dep set tight).
func asConnectError(err error, target **connect.Error) bool {
	if err == nil {
		return false
	}
	var c *connect.Error
	if errors.As(err, &c) {
		*target = c
		return true
	}
	return false
}
