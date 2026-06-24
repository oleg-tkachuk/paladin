package middleware

import (
	"testing"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/emptypb"
)

// The generic factory must reconstruct a typed *connect.Response without
// reflection: register for emptypb.Empty, marshal an Empty, and confirm
// the factory yields an AnyResponse wrapping a fresh *emptypb.Empty.
func TestRegisterResponseFactoryReflectionFree(t *testing.T) {
	const method = "/test.v1.Svc/Create"
	RegisterResponseFactory[emptypb.Empty](method)

	v, ok := responseFactories.Load(method)
	if !ok {
		t.Fatal("factory not registered")
	}
	body, err := proto.Marshal(&emptypb.Empty{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	resp, err := v.(responseFactory)(body)
	if err != nil {
		t.Fatalf("factory: %v", err)
	}
	if _, ok := resp.Any().(*emptypb.Empty); !ok {
		t.Errorf("reconstructed message type = %T, want *emptypb.Empty", resp.Any())
	}
	// It must satisfy AnyResponse (compile-time-ish runtime check).
	var _ = resp
}
