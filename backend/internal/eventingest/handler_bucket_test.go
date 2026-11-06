package eventingest

import (
	"context"
	"errors"
	"testing"
	"uuid"

	"github.com/jackc/pgx/v5"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres/sqlc"
)

// The collection the events below are for is bound to this bucket.
const boundBucket = "paladin-primary"

// An event names the bucket it came from, and only the collection's own
// bucket holds its objects: an object at the same path in another bucket is
// someone else's, and must not promote or delete this one.
func TestHandleSkipsAnEventFromAnotherBucket(t *testing.T) {
	cases := map[string]struct {
		bucket     string
		bindingErr error
		wantLookup bool
		wantErr    bool
	}{
		"the collection's bucket":            {bucket: boundBucket, wantLookup: true},
		"another bucket at the same path":    {bucket: "other-bucket"},
		"a source that does not name it":     {bucket: "", wantLookup: true},
		"a collection that does not exist":   {bucket: boundBucket, bindingErr: pgx.ErrNoRows},
		"the binding read fails, so retried": {bucket: boundBucket, bindingErr: errors.New("db down"), wantErr: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			lookup := &fakeLookup{binding: sqlc.GetCollectionRow{BackendName: "primary", BucketName: boundBucket}, bindingErr: tc.bindingErr}
			h := &PromoteHandler{Lookup: lookup, Transitioner: &refusingPromoter{}, Logger: zap.NewNop()}
			ev := uploadedEvent(uuid.New(), "docs", "a.txt")
			ev.SubjectFields.Bucket = tc.bucket

			err := h.Handle(context.Background(), ev)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Handle = %v, want error %v", err, tc.wantErr)
			}
			if lookup.lookupCalled != tc.wantLookup {
				t.Errorf("object looked up = %v, want %v", lookup.lookupCalled, tc.wantLookup)
			}
		})
	}
}
