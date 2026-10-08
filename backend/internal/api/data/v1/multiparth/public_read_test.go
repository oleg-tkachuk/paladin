package multiparth

import (
	"context"
	"regexp"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/publicread"
)

// A multipart upload into a public collection is named by the server, gets
// its public URL at initiate, and opens the store's upload with the
// collection's Cache-Control (ADR-0027).
func TestInitiateIntoAPublicCollection(t *testing.T) {
	var got InitiateArgs
	repo := &fakeRepo{public: true, cacheControl: publicread.DefaultCacheControl,
		initiateSessionFn: func(_ context.Context, a InitiateArgs, id uuid.UUID, _, _, _ string) (Session, error) {
			got = a
			return Session{ObjectID: id}, nil
		}}
	storage := &fakeStorage{}
	args := InitiateArgs{Collection: "photos", ContentType: "image/jpeg", SizeHint: 1024}
	if _, err := newHandler(repo, storage, allow()).InitiateMultipartUpload(authedCtx(uuid.New()), args); err != nil {
		t.Fatalf("Initiate: %v", err)
	}
	if !regexp.MustCompile(`^[a-z2-7]{26}$`).MatchString(got.Key) {
		t.Errorf("key %q is not one the server names a public object with", got.Key)
	}
	if !strings.HasSuffix(got.PublicURL, "/photos/"+got.Key) || storage.publicURLs != 1 {
		t.Errorf("public url %q (%d lookups), want one ending in the new key", got.PublicURL, storage.publicURLs)
	}
	if storage.cacheControl != publicread.DefaultCacheControl {
		t.Errorf("store upload opened with cache control %q, want the collection's", storage.cacheControl)
	}
}

func TestInitiateIntoAPublicCollectionRefusesAClientKey(t *testing.T) {
	repo := &fakeRepo{public: true}
	storage := &fakeStorage{}
	args := InitiateArgs{Collection: "photos", Key: "cat.jpg", ContentType: "image/jpeg", SizeHint: 1024}
	_, err := newHandler(repo, storage, allow()).InitiateMultipartUpload(authedCtx(uuid.New()), args)
	wantCode(t, err, connect.CodeFailedPrecondition)
	if storage.cacheControl != "" || storage.publicURLs != 0 {
		t.Error("a refused initiate reached the store")
	}
}

// A private collection keeps the client's key and opens the upload with no
// Cache-Control.
func TestInitiateIntoAPrivateCollectionIsUnchanged(t *testing.T) {
	var got InitiateArgs
	repo := &fakeRepo{initiateSessionFn: func(_ context.Context, a InitiateArgs, id uuid.UUID, _, _, _ string) (Session, error) {
		got = a
		return Session{ObjectID: id}, nil
	}}
	storage := &fakeStorage{}
	args := InitiateArgs{Collection: "docs", Key: "a.pdf", ContentType: "application/pdf", SizeHint: 1024}
	if _, err := newHandler(repo, storage, allow()).InitiateMultipartUpload(authedCtx(uuid.New()), args); err != nil {
		t.Fatal(err)
	}
	if got.Key != "a.pdf" || got.PublicURL != "" || storage.publicURLs != 0 {
		t.Errorf("initiated %+v, want the private upload untouched", got)
	}
}
