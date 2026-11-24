package app

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/worker"
)

// headOnly is objecth.Storage answering HEAD alone.
type headOnly struct {
	objecth.Storage
	etag string
	err  error
}

func (h headOnly) Head(context.Context, string, string, uuid.UUID, string, string, string) (string, int64, string, string, error) {
	return h.etag, 0, "", "", h.err
}

// The migration's catch-up and verify read a copy's ETag through the copier:
// the store's ETag, a confirmed miss as worker.ErrObjectMissing, anything else
// as it came.
func TestStorageCopierObjectETag(t *testing.T) {
	boom := errors.New("head: 503")
	for name, tc := range map[string]struct {
		store   headOnly
		want    string
		missing bool
		err     error
	}{
		"held":    {store: headOnly{etag: "abc"}, want: "abc"},
		"missing": {store: headOnly{err: fmt.Errorf("head: %w", objecth.ErrStoredObjectNotFound)}, missing: true},
		"failed":  {store: headOnly{err: boom}, err: boom},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := storageCopier{s: tc.store}.ObjectETag(context.Background(), worker.CopyLocation{})
			if errors.Is(err, worker.ErrObjectMissing) != tc.missing {
				t.Fatalf("err = %v, want missing: %v", err, tc.missing)
			}
			if tc.err != nil && !errors.Is(err, tc.err) {
				t.Fatalf("err = %v, want %v", err, tc.err)
			}
			if got != tc.want {
				t.Errorf("etag = %q, want %q", got, tc.want)
			}
		})
	}
}
