package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"

	iamv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1/paladiniamv1connect"
)

const settingsPath = "/paladin.iam.v1.UserSettingsService/GetForUser"

// settingsStub answers GetForUser with a name of the requested size, or
// panics, so a test reaches each of the shared options.
type settingsStub struct {
	paladiniamv1connect.UnimplementedUserSettingsServiceHandler
	nameBytes int
	panics    bool
	calls     int
}

func (s *settingsStub) GetForUser(_ context.Context, _ *connect.Request[iamv1.GetForUserRequest],
) (*connect.Response[iamv1.UserSettings], error) {
	s.calls++
	if s.panics {
		panic("boom")
	}
	return connect.NewResponse(&iamv1.UserSettings{Name: strings.Repeat("n", s.nameBytes)}), nil
}

func serveWithRPCOptions(t *testing.T, stub *settingsStub) (string, *observer.ObservedLogs) {
	t.Helper()
	core, logs := observer.New(zap.ErrorLevel)
	mux := http.NewServeMux()
	mux.Handle(paladiniamv1connect.NewUserSettingsServiceHandler(stub, rpcHandlerOptions(zap.New(core))))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL, logs
}

// postJSON makes a raw Connect JSON call and returns its status and headers;
// the body is read and closed here.
func postJSON(t *testing.T, url, body string, header http.Header) (int, http.Header) {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, url+settingsPath, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range header {
		req.Header[k] = v
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		t.Fatal(err)
	}
	return resp.StatusCode, resp.Header
}

// Without a limit connect read a request of any size into memory.
func TestAnOversizedRequestIsRefusedBeforeTheHandler(t *testing.T) {
	stub := &settingsStub{}
	url, _ := serveWithRPCOptions(t, stub)
	client := paladiniamv1connect.NewUserSettingsServiceClient(http.DefaultClient, url)
	_, err := client.GetForUser(context.Background(), connect.NewRequest(&iamv1.GetForUserRequest{
		Name: strings.Repeat("x", MaxRPCRequestBytes+1),
	}))
	if got := connect.CodeOf(err); got != connect.CodeResourceExhausted {
		t.Fatalf("code = %v (%v), want resource_exhausted", got, err)
	}
	if stub.calls != 0 {
		t.Fatalf("the handler ran for a request over the limit")
	}
}

// A panic dropped the connection; it is now an internal error, logged with
// its procedure, and the caller learns nothing of the panic.
func TestAPanicIsAnInternalErrorAndLogged(t *testing.T) {
	url, logs := serveWithRPCOptions(t, &settingsStub{panics: true})
	client := paladiniamv1connect.NewUserSettingsServiceClient(http.DefaultClient, url)
	_, err := client.GetForUser(context.Background(), connect.NewRequest(&iamv1.GetForUserRequest{Name: "u"}))
	if got := connect.CodeOf(err); got != connect.CodeInternal {
		t.Fatalf("code = %v (%v), want internal", got, err)
	}
	if strings.Contains(err.Error(), "boom") {
		t.Fatalf("the panic reached the caller: %v", err)
	}
	entries := logs.FilterField(zap.String("procedure", settingsPath)).All()
	if len(entries) != 1 || entries[0].ContextMap()["panic"] != "boom" {
		t.Fatalf("panic not logged with its procedure: %v", logs.All())
	}
}

// The strict codec stays with the other options.
func TestAnUnknownJSONFieldIsStillRefused(t *testing.T) {
	url, _ := serveWithRPCOptions(t, &settingsStub{})
	status, _ := postJSON(t, url, `{"name":"u","nope":1}`, nil)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 for an unknown field", status)
	}
}

// gzip costs more than it saves on a small response, so only a large one is
// compressed.
func TestOnlyALargeResponseIsCompressed(t *testing.T) {
	for _, tc := range []struct {
		name      string
		nameBytes int
		want      string
	}{
		{"small", RPCCompressMinBytes / 4, ""},
		{"large", RPCCompressMinBytes * 4, "gzip"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			url, _ := serveWithRPCOptions(t, &settingsStub{nameBytes: tc.nameBytes})
			_, header := postJSON(t, url, `{"name":"u"}`, http.Header{"Accept-Encoding": {"gzip"}})
			if got := header.Get("Content-Encoding"); got != tc.want {
				t.Fatalf("Content-Encoding = %q, want %q", got, tc.want)
			}
		})
	}
}

// A response over the limit fails as a resource error rather than going out.
// The limit is on the bytes sent, after compression, so the client here asks
// for none — Go's client asks for gzip unless told otherwise: the stub's response is one repeated byte, which gzip shrinks to
// nothing.
func TestAnOversizedResponseIsNotSent(t *testing.T) {
	url, _ := serveWithRPCOptions(t, &settingsStub{nameBytes: MaxRPCResponseBytes + 1})
	status, _ := postJSON(t, url, `{"name":"u"}`, http.Header{"Accept-Encoding": {"identity"}})
	if status != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 (resource_exhausted)", status)
	}
}
