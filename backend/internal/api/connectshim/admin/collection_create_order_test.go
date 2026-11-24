package admin

import (
	"context"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"go.uber.org/zap/zaptest/observer"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/tenanth"
	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

// noBinding is a tenant that has no default binding.
type noBinding struct{}

func (noBinding) GetDefaultBinding(context.Context, uuid.UUID) (tenanth.DefaultBinding, error) {
	return tenanth.DefaultBinding{}, tenanth.ErrNotFound
}

// malformedCollection breaks the collection grammar: upper case is refused.
const malformedCollection = "Not A Collection"

// CreateCollection judges the request before the tenant's state: a malformed
// collection or bucket is the caller's mistake whatever the tenant's default
// binding, and answering FailedPrecondition first hid it until the binding
// was fixed.
func TestCreateCollectionValidatesTheRequestBeforeTheBinding(t *testing.T) {
	t.Parallel()

	cases := map[string]struct {
		collection, bucket string
		want               connect.Code
	}{
		"malformed name, no bucket":    {collection: malformedCollection, want: connect.CodeInvalidArgument},
		"empty name, no bucket":        {collection: "", want: connect.CodeInvalidArgument},
		"malformed bucket":             {collection: "invoices", bucket: "buckets/b1", want: connect.CodeInvalidArgument},
		"valid name, no bucket":        {collection: "invoices", want: connect.CodeFailedPrecondition},
		"malformed name, named bucket": {collection: malformedCollection, bucket: "storageBackends/primary/buckets/b1", want: connect.CodeInvalidArgument},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			h := &recordingCollections{}
			srv := &CollectionServer{H: h, bindings: noBinding{}}
			_, err := srv.CreateCollection(context.Background(), &pb.CreateCollectionRequest{
				Parent:             "tenants/" + uuid.New().String(),
				Collection:         tc.collection,
				CollectionResource: &pb.Collection{Bucket: tc.bucket},
			})
			if got := connect.CodeOf(err); got != tc.want {
				t.Fatalf("code = %v, want %v (err: %v)", got, tc.want, err)
			}
			if h.createArgs.Collection != "" {
				t.Errorf("a refused request reached the handler: %+v", h.createArgs)
			}
		})
	}
}

// A malformed bucket used to be dropped and the collection routed to the
// tenant's default binding: a bucket the caller never named, reported as
// success.
func TestCreateCollectionRefusesAMalformedBucketRatherThanRoutingToTheDefault(t *testing.T) {
	t.Parallel()

	h := &recordingCollections{}
	srv := &CollectionServer{H: h, bindings: okBindings{}}
	_, err := srv.CreateCollection(context.Background(), &pb.CreateCollectionRequest{
		Parent:             "tenants/" + uuid.New().String(),
		Collection:         "invoices",
		CollectionResource: &pb.Collection{Bucket: "primary/b1"},
	})
	if got := connect.CodeOf(err); got != connect.CodeInvalidArgument {
		t.Fatalf("code = %v, want InvalidArgument (err: %v); routed to %q/%q",
			got, err, h.createArgs.BackendID, h.createArgs.BucketName)
	}
}

// The shim's own refusals are logged at Warn, naming the operation and the
// collection; the handler logs what it refuses itself.
func TestCreateCollectionLogsItsRefusals(t *testing.T) {
	t.Parallel()

	for name, tc := range map[string]struct{ collection string }{
		"malformed name": {collection: malformedCollection},
		"no binding":     {collection: "invoices"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			core, logs := observer.New(zapcore.DebugLevel)
			ctx := logger.WithContext(context.Background(), zap.New(core))
			srv := &CollectionServer{H: &recordingCollections{}, bindings: noBinding{}}
			if _, err := srv.CreateCollection(ctx, &pb.CreateCollectionRequest{
				Parent:             "tenants/" + uuid.New().String(),
				Collection:         tc.collection,
				CollectionResource: &pb.Collection{},
			}); err == nil {
				t.Fatal("the create was refused nowhere")
			}
			refused := logs.FilterLevelExact(zapcore.WarnLevel).
				FilterField(zap.String(logOp, opCreateCollection)).
				FilterField(zap.String(logCollection, tc.collection)).All()
			if len(refused) != 1 {
				t.Errorf("refusal logged %d times at Warn, want once: %v", len(refused), logs.All())
			}
		})
	}
}
