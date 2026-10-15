package auth

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary/unarytest"
	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1/paladindatav1connect"
)

// leavesIdempotencyKey is the key an upload carries in its body, which the
// capability interceptor reads from the request.
const leavesIdempotencyKey = "upload-1"

// uploadRecorder answers UploadObject and keeps the context it ran under.
type uploadRecorder struct {
	paladindatav1connect.UnimplementedObjectServiceHandler
	ctx context.Context
}

func (h *uploadRecorder) UploadObject(ctx context.Context, _ *datav1.UploadObjectRequest) (*datav1.UploadObjectResponse, error) {
	h.ctx = ctx
	return &datav1.UploadObjectResponse{}, nil
}

// The interceptor reads the request's idempotency key while it verifies the
// capability, and shares the request with the layers around it, so it must
// leave the request as it came.
func TestCapabilityLeavesTheRequest(t *testing.T) {
	f := newDPoPFixture(t)
	h := &uploadRecorder{}
	client := paladindatav1connect.NewObjectServiceClient(unarytest.Client(func(s *connect.Server) {
		paladindatav1connect.RegisterObjectServiceHandler(s, h)
	}, unarytest.LeavesRequest(t), CapabilityEstablishingInterceptor(f.bare, TokenPlaneData, nil, 0, nil)))
	ctx := unarytest.WithHeader(context.Background(), HeaderCapability, f.mint(t, ""))
	if _, err := client.UploadObject(ctx, &datav1.UploadObjectRequest{IdempotencyKey: leavesIdempotencyKey}); err != nil {
		t.Fatalf("call: %v", err)
	}
	if h.ctx == nil {
		t.Fatal("the handler was not reached")
	}
	if _, ok := CapabilityFromContext(h.ctx); !ok {
		t.Error("no capability was verified onto the handler's context")
	}
	if !idempotencyKeyPresent(h.ctx) {
		t.Error("the idempotency key in the request was not read")
	}
}
