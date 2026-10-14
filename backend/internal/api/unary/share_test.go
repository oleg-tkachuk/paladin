package unary_test

import (
	"context"
	"testing"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/proto"

	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1/paladindatav1connect"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary/unarytest"
)

const (
	sentParent    = "tenants/t/collections/sent"
	handlerParent = "tenants/t/collections/changed-by-handler"
)

// mutatingBatch changes the request it is handed, as a careless handler
// might, and remembers the message it got.
type mutatingBatch struct {
	paladindatav1connect.UnimplementedBatchServiceHandler
	got *datav1.BatchUpdateTagsRequest
}

func (m *mutatingBatch) BatchUpdateTags(_ context.Context, req *datav1.BatchUpdateTagsRequest) (*datav1.Operation, error) {
	m.got = req
	req.Parent = handlerParent
	return &datav1.Operation{}, nil
}

// recording is a layer that keeps the request it saw and, after the call,
// what that request held then.
type recording struct {
	seen        proto.Message
	parentAfter string
}

func (r *recording) interceptor() connect.ServerInterceptor {
	return unary.Interceptor(func(next unary.Func) unary.Func {
		return func(ctx context.Context, spec connect.Spec, req proto.Message) (proto.Message, error) {
			r.seen = req
			res, err := next(ctx, spec, req)
			r.parentAfter = req.(*datav1.BatchUpdateTagsRequest).GetParent()
			return res, err
		}
	}, nil)
}

func callBatch(t *testing.T, h *mutatingBatch, layers ...connect.ServerInterceptor) {
	t.Helper()
	client := paladindatav1connect.NewBatchServiceClient(unarytest.Client(func(s *connect.Server) {
		paladindatav1connect.RegisterBatchServiceHandler(s, h)
	}, layers...))
	if _, err := client.BatchUpdateTags(context.Background(), &datav1.BatchUpdateTagsRequest{Parent: sentParent}); err != nil {
		t.Fatal(err)
	}
}

// Every layer sees the one message decoded from the wire, not a copy each.
func TestLayersShareOneRequest(t *testing.T) {
	outer, inner := &recording{}, &recording{}
	callBatch(t, &mutatingBatch{}, outer.interceptor(), inner.interceptor())
	if outer.seen == nil || outer.seen != inner.seen {
		t.Errorf("outer saw %p, inner %p; want the same message", outer.seen, inner.seen)
	}
}

// The handler works on its own copy: what it does to its request does not
// reach a layer that reads the request after the call, as the audit log does.
func TestHandlerCannotChangeTheSharedRequest(t *testing.T) {
	outer, inner := &recording{}, &recording{}
	h := &mutatingBatch{}
	callBatch(t, h, outer.interceptor(), inner.interceptor())
	if h.got == nil || proto.Message(h.got) == inner.seen {
		t.Fatalf("handler got %p, the layers share %p; want a copy", h.got, inner.seen)
	}
	for name, r := range map[string]*recording{"outer": outer, "inner": inner} {
		if r.parentAfter != sentParent {
			t.Errorf("%s layer read parent %q after the call; want %q as sent", name, r.parentAfter, sentParent)
		}
	}
}
