package paladin_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	iamv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1/paladiniamv1connect"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// testRetryDelay keeps retry tests fast without reaching zero, which
// WithRetries reads as "use the default".
const testRetryDelay = time.Millisecond

// testCallDeadline bounds a call whose retry delay would otherwise block.
const testCallDeadline = 50 * time.Millisecond

// recorder is an IAM server that records what it was sent and fails the
// first failures calls of every RPC with failCode.
type recorder struct {
	paladiniamv1connect.UnimplementedHealthServiceHandler
	paladiniamv1connect.UnimplementedAuthServiceHandler

	mu       sync.Mutex
	headers  []http.Header
	failures int
	failCode connect.Code
	// retryAfter, when set, rides on each injected failure as Retry-After.
	retryAfter string
}

func (r *recorder) record(h http.Header) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.headers = append(r.headers, h.Clone())
	if r.failures > 0 {
		r.failures--
		err := connect.NewError(r.failCode, errors.New("injected"))
		if r.retryAfter != "" {
			err.Meta().Set(paladin.HeaderRetryAfter, r.retryAfter)
		}
		return err
	}
	return nil
}

func (r *recorder) calls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.headers)
}

func (r *recorder) last() http.Header {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.headers[len(r.headers)-1]
}

func (r *recorder) GetVersion(_ context.Context, req *connect.Request[iamv1.GetVersionRequest]) (*connect.Response[iamv1.VersionInfo], error) {
	if err := r.record(req.Header()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&iamv1.VersionInfo{}), nil
}

func (r *recorder) Login(_ context.Context, req *connect.Request[iamv1.LoginRequest]) (*connect.Response[iamv1.LoginResponse], error) {
	if err := r.record(req.Header()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&iamv1.LoginResponse{}), nil
}

func serve(t *testing.T, rec *recorder) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle(paladiniamv1connect.NewHealthServiceHandler(rec))
	mux.Handle(paladiniamv1connect.NewAuthServiceHandler(rec))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

func clients(t *testing.T, url string, opts ...paladin.Option) (paladiniamv1connect.HealthServiceClient, paladiniamv1connect.AuthServiceClient) {
	t.Helper()
	c, err := paladin.New(url, opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return paladiniamv1connect.NewHealthServiceClient(c.HTTPClient(), c.BaseURL(), c.ClientOptions()...),
		paladiniamv1connect.NewAuthServiceClient(c.HTTPClient(), c.BaseURL(), c.ClientOptions()...)
}

func TestNewRejectsBadBaseURL(t *testing.T) {
	cases := []struct {
		name string
		url  string
		want error
	}{
		{"empty", "", paladin.ErrEmptyBaseURL},
		{"blank", "   ", paladin.ErrEmptyBaseURL},
		{"relative", "admin.example.com", paladin.ErrInvalidBaseURL},
		{"wrong scheme", "ftp://admin.example.com", paladin.ErrInvalidBaseURL},
		{"no host", "https://", paladin.ErrInvalidBaseURL},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := paladin.New(tc.url); !errors.Is(err, tc.want) {
				t.Fatalf("New(%q) = %v, want %v", tc.url, err, tc.want)
			}
		})
	}
}

func TestNewRejectsZeroRetryAttempts(t *testing.T) {
	if _, err := paladin.New("https://a.example", paladin.WithRetries(0, testRetryDelay)); !errors.Is(err, paladin.ErrInvalidRetries) {
		t.Fatalf("err = %v, want ErrInvalidRetries", err)
	}
}

func TestBaseURLDropsTrailingSlash(t *testing.T) {
	c, err := paladin.New("https://admin.example.com/")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := c.BaseURL(), "https://admin.example.com"; got != want {
		t.Fatalf("BaseURL = %q, want %q", got, want)
	}
}

func TestCredentialsReachTheServer(t *testing.T) {
	rec := &recorder{}
	health, _ := clients(t, serve(t, rec),
		paladin.WithBearerToken("paladin_pat_abc"),
		paladin.WithCapability("cap-token"))

	if _, err := health.GetVersion(context.Background(), connect.NewRequest(&iamv1.GetVersionRequest{})); err != nil {
		t.Fatal(err)
	}
	h := rec.last()
	if got, want := h.Get(paladin.HeaderAuthorization), "Bearer paladin_pat_abc"; got != want {
		t.Errorf("%s = %q, want %q", paladin.HeaderAuthorization, got, want)
	}
	if got, want := h.Get(paladin.HeaderCapability), "cap-token"; got != want {
		t.Errorf("%s = %q, want %q", paladin.HeaderCapability, got, want)
	}
	if got := h.Get(paladin.HeaderIdempotencyKey); got != "" {
		t.Errorf("%s = %q without a key in the context", paladin.HeaderIdempotencyKey, got)
	}
}

func TestIdempotencyKeyReachesTheServer(t *testing.T) {
	rec := &recorder{}
	_, auth := clients(t, serve(t, rec))

	ctx := paladin.WithIdempotencyKey(context.Background(), "key-1")
	if _, err := auth.Login(ctx, connect.NewRequest(&iamv1.LoginRequest{})); err != nil {
		t.Fatal(err)
	}
	if got := rec.last().Get(paladin.HeaderIdempotencyKey); got != "key-1" {
		t.Fatalf("%s = %q, want key-1", paladin.HeaderIdempotencyKey, got)
	}
}

func TestIdempotencyKeyEmptyIsAbsent(t *testing.T) {
	if _, ok := paladin.IdempotencyKey(paladin.WithIdempotencyKey(context.Background(), "")); ok {
		t.Fatal("an empty key reported as present")
	}
	if _, ok := paladin.IdempotencyKey(context.Background()); ok {
		t.Fatal("a bare context reported a key")
	}
}

func TestRetries(t *testing.T) {
	const attempts = 3
	cases := []struct {
		name      string
		call      string
		keyed     bool
		failures  int
		failCode  connect.Code
		wantCalls int
		wantErr   bool
	}{
		{"side-effect free, transient, recovers", "GetVersion", false, 2, connect.CodeUnavailable, 3, false},
		{"side-effect free, gives up after attempts", "GetVersion", false, 5, connect.CodeUnavailable, attempts, true},
		{"rate limited is transient", "GetVersion", false, 1, connect.CodeResourceExhausted, 2, false},
		{"permanent error is not retried", "GetVersion", false, 1, connect.CodeInvalidArgument, 1, true},
		{"mutating call carries its own key and is retried", "Login", false, 1, connect.CodeUnavailable, 2, false},
		{"mutating call with a key is retried", "Login", true, 1, connect.CodeUnavailable, 2, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec := &recorder{failures: tc.failures, failCode: tc.failCode}
			health, auth := clients(t, serve(t, rec), paladin.WithRetries(attempts, testRetryDelay))

			ctx := context.Background()
			if tc.keyed {
				ctx = paladin.WithIdempotencyKey(ctx, "key-1")
			}
			var err error
			switch tc.call {
			case "GetVersion":
				_, err = health.GetVersion(ctx, connect.NewRequest(&iamv1.GetVersionRequest{}))
			case "Login":
				_, err = auth.Login(ctx, connect.NewRequest(&iamv1.LoginRequest{}))
			}
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if got := rec.calls(); got != tc.wantCalls {
				t.Fatalf("server saw %d calls, want %d", got, tc.wantCalls)
			}
		})
	}
}

// A retry that could not start before the deadline is not attempted: the
// caller gets the server's answer, not a deadline error that hides it.
func TestRetryNotAttemptedPastTheDeadline(t *testing.T) {
	rec := &recorder{failures: 10, failCode: connect.CodeUnavailable}
	health, _ := clients(t, serve(t, rec), paladin.WithRetries(10, time.Hour))

	ctx, cancel := context.WithTimeout(context.Background(), testCallDeadline)
	defer cancel()
	_, err := health.GetVersion(ctx, connect.NewRequest(&iamv1.GetVersionRequest{}))
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("err = %v, want the server's Unavailable", err)
	}
	if got := rec.calls(); got != 1 {
		t.Fatalf("server saw %d calls, want 1", got)
	}
}

func TestRetryStopsWhenContextIsCancelled(t *testing.T) {
	rec := &recorder{failures: 1000, failCode: connect.CodeUnavailable}
	health, _ := clients(t, serve(t, rec), paladin.WithRetries(1000, testRetryDelay))

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(testCallDeadline, cancel)
	_, err := health.GetVersion(ctx, connect.NewRequest(&iamv1.GetVersionRequest{}))
	if !errors.Is(err, context.Canceled) && connect.CodeOf(err) != connect.CodeCanceled {
		t.Fatalf("err = %v, want the cancellation", err)
	}
}

// The server's Retry-After is a floor on the wait: an hour does not fit in
// the deadline, so the retry is not made, where the 1ms jitter alone would.
func TestRetryHonoursRetryAfter(t *testing.T) {
	const anHour = "3600"
	rec := &recorder{failures: 1, failCode: connect.CodeResourceExhausted, retryAfter: anHour}
	health, _ := clients(t, serve(t, rec), paladin.WithRetries(3, testRetryDelay))

	ctx, cancel := context.WithTimeout(context.Background(), testCallDeadline)
	defer cancel()
	if _, err := health.GetVersion(ctx, connect.NewRequest(&iamv1.GetVersionRequest{})); connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("err = %v, want ResourceExhausted", err)
	}
	if got := rec.calls(); got != 1 {
		t.Fatalf("server saw %d calls, want 1: Retry-After was ignored", got)
	}
}

// A call with side effects gets a key of its own, and every retry of it
// carries the same one, so the server can recognise the repeat.
func TestMutatingCallKeepsOneKeyAcrossRetries(t *testing.T) {
	rec := &recorder{failures: 2, failCode: connect.CodeUnavailable}
	_, auth := clients(t, serve(t, rec), paladin.WithRetries(3, testRetryDelay))

	if _, err := auth.Login(context.Background(), connect.NewRequest(&iamv1.LoginRequest{})); err != nil {
		t.Fatal(err)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	first := rec.headers[0].Get(paladin.HeaderIdempotencyKey)
	if first == "" {
		t.Fatal("a call with side effects went out without an idempotency key")
	}
	for i, h := range rec.headers {
		if got := h.Get(paladin.HeaderIdempotencyKey); got != first {
			t.Errorf("attempt %d sent key %q, want %q", i+1, got, first)
		}
	}
}

func TestEachMutatingCallGetsItsOwnKey(t *testing.T) {
	rec := &recorder{}
	_, auth := clients(t, serve(t, rec))
	keys := map[string]bool{}
	for range 2 {
		if _, err := auth.Login(context.Background(), connect.NewRequest(&iamv1.LoginRequest{})); err != nil {
			t.Fatal(err)
		}
		keys[rec.last().Get(paladin.HeaderIdempotencyKey)] = true
	}
	if len(keys) != 2 {
		t.Errorf("two separate calls shared a key: %v", keys)
	}
}

func TestOptionsReachTheServer(t *testing.T) {
	const custom = "X-Custom"
	rec := &recorder{}
	health, _ := clients(t, serve(t, rec), paladin.WithAPIToken("paladin_pat_xyz"), paladin.WithHeader(custom, "v"))
	if _, err := health.GetVersion(context.Background(), connect.NewRequest(&iamv1.GetVersionRequest{})); err != nil {
		t.Fatal(err)
	}
	h := rec.last()
	if got := h.Get(paladin.HeaderAPIToken); got != "paladin_pat_xyz" {
		t.Errorf("%s = %q", paladin.HeaderAPIToken, got)
	}
	if got := h.Get(custom); got != "v" {
		t.Errorf("%s = %q", custom, got)
	}
	if got := h.Get(paladin.HeaderUserAgent); !strings.HasPrefix(got, "paladin-sdk-go/") {
		t.Errorf("%s = %q, want the SDK named", paladin.HeaderUserAgent, got)
	}
}

func TestClientOptionsIsACopy(t *testing.T) {
	c, err := paladin.New("https://a.example")
	if err != nil {
		t.Fatal(err)
	}
	opts := c.ClientOptions()
	opts[0] = nil
	if c.ClientOptions()[0] == nil {
		t.Fatal("mutating the returned slice changed the client")
	}
}
