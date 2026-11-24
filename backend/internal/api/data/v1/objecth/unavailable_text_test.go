package objecth

import (
	"context"
	"errors"
	"strings"
	"testing"
	"uuid"

	"connectrpc.com/connect/v2"
)

// s3Failure is what an S3 client says when the backend cannot be asked: the
// URL, the request id and the operation, none of them the caller's.
const s3Failure = "operation error S3: HeadObject, https://s3.internal:3900/paladin-shared/t/docs/a.txt, exceeded maximum number of attempts, RequestID: 1F2E"

// assertUnavailableText fails unless err is Unavailable, reads want, carries
// none of the backend's words, and keeps the backend's error as its cause.
func assertUnavailableText(t *testing.T, err error, want string) {
	t.Helper()
	cerr, ok := errors.AsType[*connect.Error](err)
	if !ok || cerr.Code() != connect.CodeUnavailable {
		t.Fatalf("err = %v, want Unavailable", err)
	}
	if cerr.Message() != want {
		t.Errorf("message %q, want %q", cerr.Message(), want)
	}
	for _, leak := range []string{"https://", "RequestID", "S3"} {
		if strings.Contains(cerr.Message(), leak) {
			t.Errorf("message %q carries %q", cerr.Message(), leak)
		}
	}
	if cerr.Unwrap() == nil || !strings.Contains(cerr.Unwrap().Error(), s3Failure) {
		t.Errorf("cause %v, want the backend's error kept for the log", cerr.Unwrap())
	}
}

func TestBytesCheckUnavailableNamesNoBackend(t *testing.T) {
	t.Parallel()
	obj := Object{ObjectID: uuid.New(), Collection: "docs", Key: "a.txt", Stored: storedIn("backend-7", "bucket-7")}
	h := &Handler{repo: &fakeObjectRepo{}, storage: headStorage{err: errors.New(s3Failure)}}
	assertUnavailableText(t, h.assertBytesPresent(context.Background(), uuid.New(), obj), msgBytesUnchecked)
}

func TestUploadCheckUnavailableNamesNoBackend(t *testing.T) {
	t.Parallel()
	assertUnavailableText(t, headFailure(errors.New(s3Failure)), msgUploadUnchecked)
}
