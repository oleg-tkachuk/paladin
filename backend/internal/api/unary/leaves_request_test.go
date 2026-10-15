package unary_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/proto"

	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary/unarytest"
)

// layerParent is what a careless layer writes over the request's parent.
const layerParent = "tenants/t/collections/changed-by-a-layer"

// recordingTB is a testing.TB whose Errorf is recorded rather than failing
// the test, so a test can check that LeavesRequest reports.
type recordingTB struct {
	testing.TB
	errors []string
}

func (r *recordingTB) Errorf(format string, args ...any) {
	r.errors = append(r.errors, fmt.Sprintf(format, args...))
}

// rewritingLayer changes the shared request before handing it on, which a
// layer must never do.
func rewritingLayer() connect.ServerInterceptor {
	return unary.Interceptor(func(next unary.Func) unary.Func {
		return func(ctx context.Context, spec connect.Spec, req proto.Message) (proto.Message, error) {
			req.(*datav1.BatchUpdateTagsRequest).Parent = layerParent
			return next(ctx, spec, req)
		}
	}, nil)
}

// readingLayer reads the request and changes nothing.
func readingLayer() connect.ServerInterceptor {
	return unary.Interceptor(func(next unary.Func) unary.Func {
		return func(ctx context.Context, spec connect.Spec, req proto.Message) (proto.Message, error) {
			_ = req.(*datav1.BatchUpdateTagsRequest).GetParent()
			return next(ctx, spec, req)
		}
	}, nil)
}

// LeavesRequest reports a layer that writes to the shared request, and only
// that: the handler, which works on its own copy, may change its request.
func TestLeavesRequest(t *testing.T) {
	for _, tc := range []struct {
		name    string
		layer   connect.ServerInterceptor
		reports bool
	}{
		{"a layer that changes the request", rewritingLayer(), true},
		{"a layer that only reads it", readingLayer(), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tb := &recordingTB{TB: t}
			callBatch(t, &mutatingBatch{}, unarytest.LeavesRequest(tb), tc.layer)
			if got := len(tb.errors) > 0; got != tc.reports {
				t.Fatalf("reported %v, want %v: %q", got, tc.reports, tb.errors)
			}
			if tc.reports && !strings.Contains(tb.errors[0], layerParent) {
				t.Errorf("report %q does not show what the layer wrote", tb.errors[0])
			}
		})
	}
}
