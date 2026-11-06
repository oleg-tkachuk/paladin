package oteltest_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"sync"
	"testing"

	"connectrpc.com/otelconnect"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladintest"
)

// traceparentHeader carries the W3C trace context.
const traceparentHeader = "Traceparent"

// presignedRequests is how many presigned requests the test sends: the
// upload's PUT and the download's GET.
const presignedRequests = 2

// tracedRequests counts the requests that left with a trace context.
type tracedRequests struct {
	next http.RoundTripper

	mu     sync.Mutex
	traced int
}

func (c *tracedRequests) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.Header.Get(traceparentHeader) != "" {
		c.mu.Lock()
		c.traced++
		c.mu.Unlock()
	}
	return c.next.RoundTrip(r)
}

func (c *tracedRequests) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.traced
}

// OpenTelemetry needs nothing from the SDK: connect's own otelconnect
// interceptor and otelhttp's transport plug into the options there are, and
// carry the W3C trace context to the server and to storage.
func TestOpenTelemetryThroughTheExtensionPoints(t *testing.T) {
	spans := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
	propagator := propagation.TraceContext{}
	interceptor, err := otelconnect.NewClientInterceptor(
		otelconnect.WithTracerProvider(provider), otelconnect.WithPropagator(propagator))
	if err != nil {
		t.Fatal(err)
	}
	storage := &tracedRequests{next: http.DefaultTransport}
	transfer, err := paladin.NewTransfer(paladin.WithTransferHTTPClient(&http.Client{
		Transport: otelhttp.NewTransport(storage,
			otelhttp.WithTracerProvider(provider), otelhttp.WithPropagators(propagator)),
	}))
	if err != nil {
		t.Fatal(err)
	}

	srv := paladintest.New(t)
	p := srv.Connect(paladin.WithInterceptors(interceptor), paladin.WithTransfer(transfer))
	ctx := context.Background()
	body := []byte("traced")
	obj, err := paladin.Upload(ctx, p.Data, paladin.UploadInput{
		Parent: srv.Collection().String(), Key: "k", ContentType: "text/plain",
		Size: int64(len(body)), Body: bytes.NewReader(body),
	}, paladin.UploadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	r, err := paladin.Download(ctx, p.Data, obj.GetName(), paladin.DownloadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(io.Discard, r); err != nil {
		t.Fatal(err)
	}
	if err := r.Close(); err != nil {
		t.Fatal(err)
	}

	// The RPC leg.
	rpcs := srv.Requests()
	if len(rpcs) == 0 {
		t.Fatal("the fake saw no RPC")
	}
	for _, req := range rpcs {
		if req.Header.Get(traceparentHeader) == "" {
			t.Errorf("%s carried no traceparent", req.Procedure)
		}
	}
	// The presigned leg.
	if got := storage.count(); got != presignedRequests {
		t.Errorf("%d presigned requests carried a traceparent, want the PUT's and the GET's", got)
	}
	if got, want := len(spans.Ended()), len(rpcs)+presignedRequests; got < want {
		t.Errorf("%d spans ended, want at least %d: every RPC's and both transfers'", got, want)
	}
}
