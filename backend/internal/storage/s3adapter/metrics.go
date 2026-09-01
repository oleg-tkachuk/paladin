package s3adapter

import (
	"context"
	"time"

	"github.com/aws/smithy-go/middleware"

	awsmiddleware "github.com/aws/aws-sdk-go-v2/aws/middleware"

	"github.com/oleg-tkachuk/paladin/internal/metrics"
)

// withCallMetrics instruments every call this client makes to its storage
// backend.
//
// It is an SDK middleware rather than a wrapper around each Client method, and
// that is the whole point: there are fifteen-odd methods on Client and any new
// one would have to remember to instrument itself. A middleware covers the
// operations that exist, the ones added later, and the ones the SDK issues on
// its own — and it gets the operation name from the SDK instead of a string
// the author typed.
//
// Installed on the Initialize step, which wraps the ENTIRE operation including
// the SDK's own retries. Deserialize would give cleaner per-attempt latency;
// Initialize gives the number the caller actually waited, and "the caller
// waited" is the question a degrading backend raises.
//
// The presign client is deliberately left uninstrumented: presigning is local
// signing, it makes no network call, and paladin_presign_total already counts
// it. Counting it here would inflate the storage-call rate with work no
// storage backend ever saw.
func withCallMetrics(backendID *string) func(*middleware.Stack) error {
	return func(stack *middleware.Stack) error {
		return stack.Initialize.Add(
			middleware.InitializeMiddlewareFunc(
				"PaladinCallMetrics",
				func(
					ctx context.Context,
					in middleware.InitializeInput,
					next middleware.InitializeHandler,
				) (middleware.InitializeOutput, middleware.Metadata, error) {
					start := time.Now()
					out, md, err := next.HandleInitialize(ctx, in)
					outcome := "ok"
					if err != nil {
						outcome = "error"
					}
					id := ""
					if backendID != nil {
						id = *backendID
					}
					metrics.RecordStorageCall(
						ctx, id,
						awsmiddleware.GetOperationName(ctx),
						outcome,
						time.Since(start).Seconds(),
					)
					return out, md, err
				},
			),
			middleware.Before,
		)
	}
}
