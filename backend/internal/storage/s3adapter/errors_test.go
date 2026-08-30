package s3adapter

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/google/uuid"
)

// The asymmetry these tests defend: a false negative costs a retry, a false
// positive costs data. ReconcilerV2 turns ErrObjectNotFound into
// PENDING → FAILED, and FAILED objects stop being served and become eligible
// for reclamation — so every case below that is NOT a plain missing object
// asserts the sentinel is absent, and those are the assertions that matter.

// TestHeadWrapsNotFound drives a real aws-sdk-go-v2 client against a backend
// answering 404 to HEAD, which is the exact wire exchange the reconciler saw
// 174 times in four minutes while MarkFailed stayed unreachable.
//
// The fake 404s the OBJECT HEAD only and lets the bucket HEAD succeed, which
// is what "this key is gone" actually looks like on the wire. That matters
// since Head grew its bucket-reachability guard: 404-ing both is now the
// missing-BUCKET case, and it is asserted separately, as a non-sentinel error,
// by TestHeadDistinguishesMissingBucketFromMissingObject. So this test is also
// the proof that the guard does not swallow a genuine absent key — without
// which the reconciler could never finish an abandoned upload.
func TestHeadWrapsNotFound(t *testing.T) {
	f := newFakeS3(t)
	f.route = func(w http.ResponseWriter, r *http.Request, _, key string) bool {
		if r.Method == http.MethodHead && key != "" {
			// A HEAD carries no body, so the backend can only signal through
			// the status line — the case that made the SDK's typed error the
			// only thing left to classify on.
			w.WriteHeader(http.StatusNotFound)
			return true
		}
		return false
	}
	c := newTestClient(t, f.srv.URL)

	_, _, _, _, err := c.Head(context.Background(), "bkt", uuid.New(), "ok-1", "k-1")
	if err == nil {
		t.Fatal("Head against a 404 backend: want an error")
	}
	if !errors.Is(err, ErrObjectNotFound) {
		t.Errorf("errors.Is(err, ErrObjectNotFound) = false; want true.\ngot: %v", err)
	}
	// The sentinel must not swallow the diagnosis: the operator log needs the
	// status code and operation name to tell a missing object from a broken
	// route. Two %w verbs, so both survive.
	if msg := err.Error(); !strings.Contains(msg, "404") || !strings.Contains(msg, "HeadObject") {
		t.Errorf("wrapped error lost the underlying detail: %q", msg)
	}
}

// TestHeadDoesNotWrapTransientFailures is the safety half. Every one of these
// is a live backend that could not answer right now — marking objects FAILED
// on any of them would reap data that exists.
func TestHeadDoesNotWrapTransientFailures(t *testing.T) {
	cases := []struct {
		name   string
		status int
		s3code string
	}{
		{"access denied", http.StatusForbidden, "AccessDenied"},
		{"credentials rejected", http.StatusForbidden, "InvalidAccessKeyId"},
		{"internal error", http.StatusInternalServerError, "InternalError"},
		{"throttled", http.StatusServiceUnavailable, "SlowDown"},
		{"gateway timeout", http.StatusGatewayTimeout, "RequestTimeout"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newFakeS3(t)
			f.route = func(w http.ResponseWriter, r *http.Request, _, _ string) bool {
				if r.Method == http.MethodHead {
					writeS3Error(w, tc.status, tc.s3code, "injected")
					return true
				}
				return false
			}
			c := newTestClient(t, f.srv.URL)

			_, _, _, _, err := c.Head(context.Background(), "bkt", uuid.New(), "ok-1", "k-1")
			if err == nil {
				t.Fatal("want an error")
			}
			if errors.Is(err, ErrObjectNotFound) {
				t.Errorf("%s classified as not-found — this would mark live objects FAILED.\ngot: %v",
					tc.name, err)
			}
		})
	}
}

// TestHeadDistinguishesMissingBucketFromMissingObject pins the guard that
// closes a data-safety hole this test used to merely document.
//
// A HEAD response has no body, so aws-sdk-go-v2's HeadObject deserialiser
// never reads an error code — it synthesises *s3types.NotFound from the 404
// status alone. A backend answering "that bucket does not exist" therefore
// arrives at notFound byte-identical to "that key does not exist", even when
// the server did send a NoSuchBucket code, as this test's fake does. The
// classifier cannot separate them and never could; Head resolves it one layer
// up, by asking HeadBucket before it lets the sentinel out.
//
// What that buys: a wrong or deleted bucket binding no longer makes
// ReconcilerV2 mark the binding's pending-expired objects FAILED. The 404
// becomes a retryable error, which is what an unexplained 404 is.
//
// notFound's bucket-level rejection stays load-bearing — it fires whenever the
// error does carry a code, which is every path except a bodiless HEAD, and it
// is the cheaper of the two checks. TestNotFoundClassifier covers it directly.
func TestHeadDistinguishesMissingBucketFromMissingObject(t *testing.T) {
	f := newFakeS3(t)
	// Every HEAD 404s — the object HEAD and the guard's bucket HEAD alike.
	// That is the shape of a vanished bucket.
	f.route = func(w http.ResponseWriter, r *http.Request, _, _ string) bool {
		if r.Method == http.MethodHead {
			writeS3Error(w, http.StatusNotFound, "NoSuchBucket", "no such bucket")
			return true
		}
		return false
	}
	c := newTestClient(t, f.srv.URL)

	_, _, _, _, err := c.Head(context.Background(), "gone-bkt", uuid.New(), "ok-1", "k-1")
	if err == nil {
		t.Fatal("want an error")
	}
	if errors.Is(err, ErrObjectNotFound) {
		t.Fatalf("a missing bucket was reported as a missing object — the "+
			"reconciler would mark this binding's objects FAILED.\ngot: %v", err)
	}
	if !strings.Contains(err.Error(), "unreachable") {
		t.Errorf("error should say the bucket is unreachable.\ngot: %v", err)
	}

	// The guard must be the reason, not a coincidence: a bucket-level HEAD
	// (path-style, so an empty key) has to have been issued.
	var bucketHeads int
	for _, r := range f.requestsFor(http.MethodHead, "") {
		if r.Key == "" {
			bucketHeads++
		}
	}
	if bucketHeads != 1 {
		t.Errorf("bucket-level HEAD count = %d, want 1", bucketHeads)
	}
}

// TestHeadUnreachableBackendIsNotNotFound covers the transport layer: no
// response at all must never look like "the object is absent". A backend
// outage that reaped every pending object would be the worst possible reading
// of this signal.
func TestHeadUnreachableBackendIsNotNotFound(t *testing.T) {
	// A port nothing listens on — connection refused, no HTTP exchange.
	c := newTestClient(t, "http://127.0.0.1:1")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, _, _, err := c.Head(ctx, "bkt", uuid.New(), "ok-1", "k-1")
	if err == nil {
		t.Fatal("Head against a dead endpoint: want an error")
	}
	if errors.Is(err, ErrObjectNotFound) {
		t.Errorf("unreachable backend classified as not-found.\ngot: %v", err)
	}
}

// TestNotFoundClassifier exercises notFound directly on error shapes the fake
// backend cannot produce — the SDK's typed errors, a context deadline, a DNS
// failure — so the layering (bucket-level rejects before object-level
// accepts) is pinned independently of what a server chooses to send.
func TestNotFoundClassifier(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"typed NotFound", &s3types.NotFound{}, true},
		{"typed NoSuchKey", &s3types.NoSuchKey{}, true},
		{"typed NoSuchBucket", &s3types.NoSuchBucket{}, false},
		{"wrapped typed NotFound", fmt.Errorf("head: %w", &s3types.NotFound{}), true},
		{"doubly wrapped", fmt.Errorf("outer: %w", fmt.Errorf("head: %w", &s3types.NotFound{})), true},
		{"context deadline", context.DeadlineExceeded, false},
		{"context cancelled", context.Canceled, false},
		{"dns failure", &net.DNSError{Err: "no such host", IsNotFound: true}, false},
		{"plain error", errors.New("something went wrong"), false},
		{"status 404 without a code", fakeStatusError{status: http.StatusNotFound}, true},
		{"status 403 without a code", fakeStatusError{status: http.StatusForbidden}, false},
		{"code beats status: 404 + AccessDenied", fakeAPIError{code: "AccessDenied", status: 404}, false},
		{"code beats status: 404 + NoSuchBucket", fakeAPIError{code: "NoSuchBucket", status: 404}, false},
		{"code NotFound", fakeAPIError{code: "NotFound", status: 404}, true},
		{"unknown code with 404", fakeAPIError{code: "WeirdBackendCode", status: 404}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := notFound(tc.err); got != tc.want {
				t.Errorf("notFound(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

// A net.DNSError with IsNotFound=true is the trap this pins: it satisfies a
// "not found" reading in plain English and means the opposite of an absent
// object. It has neither ErrorCode nor HTTPStatusCode, so it falls through to
// the transport-level default — but only because that default is `false`.

// fakeStatusError carries an HTTP status and no error code — the shape a
// backend produces when the SDK could not map its response to a typed error.
type fakeStatusError struct{ status int }

func (e fakeStatusError) Error() string       { return fmt.Sprintf("http %d", e.status) }
func (e fakeStatusError) HTTPStatusCode() int { return e.status }

// fakeAPIError carries both, so the code-before-status ordering is testable.
type fakeAPIError struct {
	code   string
	status int
}

func (e fakeAPIError) Error() string        { return e.code }
func (e fakeAPIError) ErrorCode() string    { return e.code }
func (e fakeAPIError) ErrorMessage() string { return "" }
func (e fakeAPIError) HTTPStatusCode() int  { return e.status }
