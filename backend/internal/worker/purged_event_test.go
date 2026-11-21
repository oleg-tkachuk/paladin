package worker

import (
	"maps"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/eventtype"
)

func TestObjectPurgedPayload_NamesEachFactUnderTheSharedKey(t *testing.T) {
	o := purgedObject{
		TenantID: uuid.New(), ObjectID: uuid.New(), Collection: "docs",
		Key: "a/b.pdf", Backend: "primary", Bucket: "paladin-test",
	}
	want := map[string]any{
		eventtype.PayloadTenantID:   o.TenantID.String(),
		eventtype.PayloadCollection: o.Collection,
		eventtype.PayloadKey:        o.Key,
		eventtype.PayloadObjectID:   o.ObjectID.String(),
		eventtype.PayloadBackendID:  o.Backend,
		eventtype.PayloadBucket:     o.Bucket,
		eventtype.PayloadReclaimed:  true,
	}
	if got := objectPurgedPayload(o); !maps.Equal(got, want) {
		t.Errorf("payload = %v, want %v", got, want)
	}
}
