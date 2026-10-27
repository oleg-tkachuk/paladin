package data

import (
	"context"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/batchh"
	pb "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
)

// recordingBatch keeps the copy it was asked for.
type recordingBatch struct {
	failingBatch
	copied *batchh.BatchCopyArgs
}

func (r *recordingBatch) BatchCopy(_ context.Context, args batchh.BatchCopyArgs) (uuid.UUID, error) {
	r.copied = &args
	return uuid.New(), nil
}

// The copy's key is the prefix followed by the source key, taken literally:
// the field once promised a CEL expression it never evaluated, and a caller
// who sent `"backup/" + key` got that text in front of every key.
func TestBatchCopyTakesTheKeyPrefixLiterally(t *testing.T) {
	ctx := ctxTenant(tenantA)
	parent := "tenants/" + tenantA.String() + "/collections/c1"
	objName := parent + "/objects/" + objUUID.String()
	for _, prefix := range []string{"backup/2026/", ""} {
		b := &recordingBatch{}
		if _, err := (&BatchServer{H: b}).BatchCopyObjects(ctx, &pb.BatchCopyObjectsRequest{
			SourceParent: parent, DestinationCollection: parent,
			Selector:             &pb.ObjectSelector{Names: []string{objName}},
			DestinationKeyPrefix: prefix,
		}); err != nil {
			t.Fatalf("prefix %q: %v", prefix, err)
		}
		if b.copied == nil || b.copied.KeyPrefix != prefix {
			t.Errorf("prefix %q reached the handler as %+v", prefix, b.copied)
		}
	}
}
