package unary_test

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"

	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1/paladindatav1connect"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/unary/unarytest"
)

// The data plane runs this many unary interceptors (capability, acting
// tenant, quota, idempotency, audit).
const dataPlaneUnaryLayers = 5

// tagValueBytes sizes each tag of the benchmark request; with
// paladin.MaxRequestBytes it decides how many tags fill one.
const tagValueBytes = 1 << 10

type batchTags struct {
	paladindatav1connect.UnimplementedBatchServiceHandler
}

func (batchTags) BatchUpdateTags(context.Context, *datav1.BatchUpdateTagsRequest) (*datav1.Operation, error) {
	return &datav1.Operation{}, nil
}

// maxSizeBatch is a BatchUpdateTags request just under the request size
// limit — the case the interceptor chain's handling of the message would show
// in, if anywhere.
func maxSizeBatch() *datav1.BatchUpdateTagsRequest {
	value := strings.Repeat("v", tagValueBytes)
	// One tag short of the limit, which also leaves room for the keys.
	count := paladin.MaxRequestBytes/tagValueBytes - 1
	tags := make(map[string]string, count)
	for i := range count {
		tags[fmt.Sprintf("k%06d", i)] = value
	}
	return &datav1.BatchUpdateTagsRequest{Parent: "tenants/t/collections/c", Tags: tags}
}

// BenchmarkDataPlaneChain measures a maximum-size request through as many
// pass-through unary interceptors as the data plane runs, against the same
// call with none: the difference is what the chain costs.
func BenchmarkDataPlaneChain(b *testing.B) {
	for _, layers := range []int{0, dataPlaneUnaryLayers} {
		b.Run(fmt.Sprintf("layers=%d", layers), func(b *testing.B) {
			benchmarkChain(b, layers)
		})
	}
}

func benchmarkChain(b *testing.B, count int) {
	b.Helper()
	pass := unary.Interceptor(func(next unary.Func) unary.Func { return next }, nil)
	layers := make([]connect.ServerInterceptor, count)
	for i := range layers {
		layers[i] = pass
	}
	client := paladindatav1connect.NewBatchServiceClient(unarytest.Client(func(s *connect.Server) {
		paladindatav1connect.RegisterBatchServiceHandler(s, batchTags{})
	}, layers...))
	req := maxSizeBatch()
	ctx := context.Background()
	b.ReportAllocs()
	for b.Loop() {
		if _, err := client.BatchUpdateTags(ctx, req); err != nil {
			b.Fatal(err)
		}
	}
}
