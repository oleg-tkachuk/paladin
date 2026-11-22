package multiparth

import (
	"context"
	"errors"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/storage/storagepath"
)

func TestInitiateRefusesANonCanonicalKey(t *testing.T) {
	for _, key := range []string{"a//b", "a/./b", "/a", "..", "dir/"} {
		t.Run(key, func(t *testing.T) {
			opened := false
			repo := &fakeRepo{initiateSessionFn: func(context.Context, InitiateArgs, uuid.UUID, string, string, string) (Session, error) {
				opened = true
				return Session{}, nil
			}}
			storage := &fakeStorage{}
			args := InitiateArgs{Collection: "docs", Key: key, ContentType: "application/pdf", SizeHint: 1024}
			_, err := newHandler(repo, storage, allow()).InitiateMultipartUpload(authedCtx(uuid.New()), args)
			wantCode(t, err, connect.CodeInvalidArgument)
			if !errors.Is(err, storagepath.ErrNotCanonical) {
				t.Errorf("err = %v, want the key refusal", err)
			}
			if opened {
				t.Error("a session was opened for a refused key")
			}
		})
	}
}

// An initiate that names no key gets one, as an upload does: an empty key
// would compose to the collection's own prefix, shared by every such upload.
func TestInitiateNamesAKeyLeftEmpty(t *testing.T) {
	var got InitiateArgs
	repo := &fakeRepo{initiateSessionFn: func(_ context.Context, a InitiateArgs, id uuid.UUID, _, _, _ string) (Session, error) {
		got = a
		return Session{ObjectID: id}, nil
	}}
	args := InitiateArgs{Collection: "docs", ContentType: "application/pdf", SizeHint: 1024}
	if _, err := newHandler(repo, &fakeStorage{}, allow()).InitiateMultipartUpload(authedCtx(uuid.New()), args); err != nil {
		t.Fatal(err)
	}
	if err := storagepath.ValidateKey(got.Key); err != nil {
		t.Errorf("initiated with key %q: %v", got.Key, err)
	}
}
