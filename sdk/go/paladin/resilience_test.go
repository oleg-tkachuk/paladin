package paladin_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladintest"
)

// faultOnce answers the first n storage requests matching match with status
// and body, then lets the fake answer.
func faultOnce(n int, match func(*http.Request) bool, status int, body string) paladintest.StorageFault {
	var mu sync.Mutex
	left := n
	return func(r *http.Request) (int, string) {
		mu.Lock()
		defer mu.Unlock()
		if left > 0 && match(r) {
			left--
			return status, body
		}
		return 0, ""
	}
}

func isPut(r *http.Request) bool { return r.Method == http.MethodPut }
func isGet(r *http.Request) bool { return r.Method == http.MethodGet }
func isPart(n string) func(*http.Request) bool {
	return func(r *http.Request) bool { return r.Method == http.MethodPut && r.URL.Query().Get("part") == n }
}

func countRPC(srv *paladintest.Server, suffix string) int {
	n := 0
	for _, r := range srv.Requests() {
		if strings.HasSuffix(r.Procedure, suffix) {
			n++
		}
	}
	return n
}

// An upload URL that expired before the PUT used to fail the upload. It is
// now presigned again — RegenerateUploadUrl — and the PUT repeated.
func TestUploadRepresignsAnExpiredURL(t *testing.T) {
	srv := paladintest.New(t)
	srv.FailStorage(faultOnce(1, isPut, http.StatusForbidden, paladintest.ExpiredBody))
	p := srv.Connect()
	body := []byte("expired once")

	obj, err := paladin.Upload(context.Background(), p.Data, paladin.UploadInput{
		Parent: srv.Collection().String(), Key: "k", ContentType: "text/plain",
		Size: int64(len(body)), Body: bytes.NewReader(body),
	}, paladin.UploadOptions{})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if got, _ := srv.Content(obj.GetName()); !bytes.Equal(got, body) {
		t.Fatalf("stored %q", got)
	}
	if n := countRPC(srv, "/RegenerateUploadUrl"); n != 1 {
		t.Fatalf("RegenerateUploadUrl called %d times, want 1", n)
	}
}

// A PUT that landed but whose answer was lost is retried, and the URL's
// If-None-Match answers 412: that is success, and the upload completes.
func TestUploadCompletesWhenARetriedPUTFindsItStored(t *testing.T) {
	srv := paladintest.New(t)
	srv.FailStorageAfterStoring(faultOnce(1, isPut, http.StatusServiceUnavailable, "lost answer"))
	p := srv.Connect()
	body := []byte("landed")
	obj, err := paladin.Upload(context.Background(), p.Data, paladin.UploadInput{
		Parent: srv.Collection().String(), Key: "k", ContentType: "text/plain",
		Size: int64(len(body)), Body: bytes.NewReader(body),
	}, paladin.UploadOptions{})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if got, _ := srv.Content(obj.GetName()); !bytes.Equal(got, body) {
		t.Fatalf("stored %q", got)
	}
	if n := len(srv.StorageOps()); n != 2 {
		t.Fatalf("storage saw %d PUTs, want the lost one and its 412 retry", n)
	}
}

// A part refused by a busy store is presigned again and resent; the upload
// completes instead of being aborted.
func TestUploadRetriesAFailedPart(t *testing.T) {
	srv := paladintest.New(t)
	srv.FailStorage(faultOnce(2, isPart("2"), http.StatusServiceUnavailable, "SlowDown"))
	p := srv.Connect()
	body := bytes.Repeat([]byte("r"), 2*paladintest.PartSize+3)

	obj, err := paladin.Upload(context.Background(), p.Data, paladin.UploadInput{
		Parent: srv.Collection().String(), Key: "big", ContentType: "application/octet-stream",
		Size: int64(len(body)), Body: bytes.NewReader(body),
	}, paladin.UploadOptions{MultipartThreshold: paladintest.PartSize})
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if got, _ := srv.Content(obj.GetName()); !bytes.Equal(got, body) {
		t.Fatal("stored content differs")
	}
}

// A refusal no retry can fix is not retried: one attempt, then the error.
func TestUploadDoesNotRetryAPermanentRefusal(t *testing.T) {
	srv := paladintest.New(t)
	srv.FailStorage(faultOnce(100, isPut, http.StatusForbidden, "<Error><Code>AccessDenied</Code></Error>"))
	p := srv.Connect()
	_, err := paladin.Upload(context.Background(), p.Data, paladin.UploadInput{
		Parent: srv.Collection().String(), Key: "k", ContentType: "text/plain", Size: 1, Body: bytes.NewReader([]byte("x")),
	}, paladin.UploadOptions{})
	var te *paladin.TransferError
	if !errors.As(err, &te) || te.Status != http.StatusForbidden {
		t.Fatalf("err = %v, want the 403", err)
	}
	if n := len(srv.StorageOps()); n != 1 {
		t.Fatalf("storage saw %d requests, want 1", n)
	}
}

// An interrupted multipart upload resumes from the parts storage holds: only
// the missing ones are sent, and the session is left open for the resume
// rather than aborted.
func TestUploadResumesAnInterruptedMultipart(t *testing.T) {
	srv := paladintest.New(t)
	p := srv.Connect()
	body := bytes.Repeat([]byte("m"), 2*paladintest.PartSize+11)
	in := paladin.UploadInput{
		Parent: srv.Collection().String(), Key: "big", ContentType: "application/octet-stream",
		Size: int64(len(body)), Body: bytes.NewReader(body),
	}
	var session paladin.UploadSession
	// Parts 2 and 3 are refused for good: the first run fails after part 1.
	srv.FailStorage(func(r *http.Request) (int, string) {
		if isPart("2")(r) || isPart("3")(r) {
			return http.StatusBadRequest, "refused"
		}
		return 0, ""
	})
	_, err := paladin.Upload(context.Background(), p.Data, in, paladin.UploadOptions{
		MultipartThreshold: paladintest.PartSize, PartConcurrency: 1,
		OnSession: func(s paladin.UploadSession) { session = s },
	})
	if err == nil {
		t.Fatal("the first run succeeded")
	}
	if session.UploadID == "" || session.TotalParts != 3 {
		t.Fatalf("session = %+v", session)
	}
	if n := countRPC(srv, "/AbortMultipartUpload"); n != 0 {
		t.Fatal("a resumable upload was aborted")
	}

	srv.FailStorage(nil)
	before := len(srv.StorageOps())
	obj, err := paladin.Upload(context.Background(), p.Data, in, paladin.UploadOptions{
		MultipartThreshold: paladintest.PartSize, Resume: &session,
	})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	var sent []string
	for _, op := range srv.StorageOps()[before:] {
		sent = append(sent, op.Path)
	}
	for _, path := range sent {
		if strings.HasSuffix(path, "part=1") {
			t.Fatalf("part 1 was sent again on resume: %v", sent)
		}
	}
	if len(sent) != 2 {
		t.Fatalf("resume sent %v, want parts 2 and 3", sent)
	}
	if got, _ := srv.Content(obj.GetName()); !bytes.Equal(got, body) {
		t.Fatal("resumed content differs")
	}
}

// A download URL that expired before the GET is requested again.
func TestDownloadRepresignsAnExpiredURL(t *testing.T) {
	srv := paladintest.New(t)
	obj := srv.Put(srv.Collection(), "k", "text/plain", []byte("again"))
	srv.FailStorage(faultOnce(1, isGet, http.StatusForbidden, paladintest.ExpiredBody))
	p := srv.Connect()
	b, err := paladin.Download(context.Background(), p.Data, obj.GetName(), paladin.DownloadOptions{})
	if err != nil {
		t.Fatalf("Download: %v", err)
	}
	defer func() { _ = b.Close() }()
	if n := countRPC(srv, "/DownloadObject"); n != 2 {
		t.Fatalf("DownloadObject called %d times, want 2", n)
	}
}
