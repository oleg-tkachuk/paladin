package collectionh

import (
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/storage/storagepath"
	"github.com/oleg-tkachuk/paladin/backend/tests/fuzzassert"
)

// createCodes is what CreateCollection answers a malformed collection with: a
// name, binding, visibility or Cache-Control it refuses.
var createCodes = []connect.Code{connect.CodeInvalidArgument, connect.CodeFailedPrecondition}

// Whatever name, binding and visibility a create names, CreateCollection
// either stores a collection the collections_name_format CHECK accepts or
// refuses it as the caller's mistake, in words that name nothing of the
// server.
func FuzzCreateCollection(f *testing.F) {
	for _, c := range []struct {
		name, displayName, backend, bucket, cacheControl string
		public                                           bool
	}{
		{"docs", "Docs", "aws-eu", "paladin-prod", "", false},
		{"e2e/logs", "", "b", "n", "", false},
		{"acme-avatars", "", "aws-eu", "public", "public, max-age=60", true},
		{"docs", "", "", "", "", false},
		{"docs", "", "b", "n", "max-age=60", false},
		{"Docs", "", "b", "n", "", false},
		// A malformed name beside a visibility rule it also breaks.
		{"Docs", "", "b", "n", "max-age=60", false},
		{"a//b", "x\x00", "b", "n", "no-store\n", true},
	} {
		f.Add(c.name, c.displayName, c.backend, c.bucket, c.cacheControl, c.public)
	}
	f.Fuzz(func(t *testing.T, name, displayName, backend, bucket, cacheControl string, public bool) {
		repo := creatingRepo()
		_, err := NewHandler(repo, allowAll()).CreateCollection(authedCtx(uuid.New()), CreateCollectionArgs{
			Collection: name, DisplayName: displayName, BackendID: backend, BucketName: bucket,
			PublicRead: public, CacheControl: cacheControl,
		})
		fuzzassert.Refusal(t, err, createCodes, name, displayName, backend, bucket, cacheControl)
		// A malformed name is the request's fault whatever else is wrong, so
		// no binding or visibility rule may answer for it first.
		if storagepath.ValidateCollection(name) != nil && connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("collection %q answered %v, want InvalidArgument (err: %v)", name, connect.CodeOf(err), err)
		}
		if err != nil {
			return
		}
		if verr := storagepath.ValidateCollection(repo.lastCreate.Collection); verr != nil {
			t.Fatalf("collection %q reached the store: %v", repo.lastCreate.Collection, verr)
		}
	})
}
