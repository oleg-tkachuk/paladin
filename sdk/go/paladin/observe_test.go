package paladin_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/otelconnect"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/propagation"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
	iamv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1/paladiniamv1connect"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// records is an slog handler that keeps every record.
type records struct {
	mu   sync.Mutex
	list []slog.Record
}

func (r *records) Enabled(context.Context, slog.Level) bool { return true }
func (r *records) Handle(_ context.Context, rec slog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.list = append(r.list, rec)
	return nil
}
func (r *records) WithAttrs([]slog.Attr) slog.Handler { return r }
func (r *records) WithGroup(string) slog.Handler      { return r }

func (r *records) levels() []slog.Level {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []slog.Level
	for _, rec := range r.list {
		out = append(out, rec.Level)
	}
	return out
}

func TestUserAgentSuffix(t *testing.T) {
	rec := &recorder{}
	health, _ := clients(t, serve(t, rec), paladin.WithUserAgentSuffix("gateway/1.4"))
	if _, err := health.GetVersion(context.Background(), &iamv1.GetVersionRequest{}); err != nil {
		t.Fatal(err)
	}
	ua := rec.last().Get(paladin.HeaderUserAgent)
	if !strings.HasPrefix(ua, "paladin-sdk-go/") || !strings.HasSuffix(ua, " gateway/1.4") {
		t.Errorf("User-Agent = %q, want the SDK's then the suffix", ua)
	}
}

func TestRetriesAreReportedToHooksAndTheLogger(t *testing.T) {
	rec := &recorder{failures: 2, failCode: connect.CodeUnavailable}
	var events []paladin.RetryEvent
	logs := &records{}
	health, _ := clients(t, serve(t, rec), paladin.WithRetries(3, testRetryDelay),
		paladin.WithHooks(paladin.Hooks{OnRetry: func(e paladin.RetryEvent) { events = append(events, e) }}),
		paladin.WithLogger(slog.New(logs)))
	if _, err := health.GetVersion(context.Background(), &iamv1.GetVersionRequest{}); err != nil {
		t.Fatal(err)
	}
	if len(events) != 2 || events[0].Attempt != 1 || events[1].Attempt != 2 ||
		events[0].Procedure != paladiniamv1connect.HealthServiceGetVersionProcedure || connect.CodeOf(events[0].Err) != connect.CodeUnavailable {
		t.Errorf("retry events = %+v", events)
	}
	if got := logs.levels(); len(got) != 2 || got[0] != slog.LevelDebug {
		t.Errorf("logged %v, want two debug records", got)
	}
}

func TestTransfersAreReportedToHooksAndTheLogger(t *testing.T) {
	var (
		mu     sync.Mutex
		events []paladin.TransferEvent
	)
	logs := &records{}
	tr := mustTransfer(t,
		paladin.WithTransferHooks(paladin.Hooks{OnTransfer: func(e paladin.TransferEvent) {
			mu.Lock()
			defer mu.Unlock()
			events = append(events, e)
		}}),
		paladin.WithTransferLogger(slog.New(logs)))
	data, dp, st := newTransfer(t, 0, paladin.WithTransfer(tr))
	body := []byte("counted bytes")
	obj := upload(t, data, "k", body)
	if _, err := readAll(t, data, obj.GetName(), paladin.DownloadOptions{}); err != nil {
		t.Fatal(err)
	}
	// A download whose content does not match is reported as failed.
	dp.described = &datav1.Object{Name: obj.GetName(), SizeBytes: int64(len(body)) + 1}
	if _, err := readAll(t, data, obj.GetName(), paladin.DownloadOptions{}); err == nil {
		t.Fatal("a short download was not refused")
	}
	// So is one storage refuses.
	st.refuseWith(http.StatusForbidden)
	_, _ = readAll(t, data, obj.GetName(), paladin.DownloadOptions{})

	mu.Lock()
	defer mu.Unlock()
	if len(events) != 4 {
		t.Fatalf("events = %+v, want PUT, GET, failed GET, refused GET", events)
	}
	put, get, short, refused := events[0], events[1], events[2], events[3]
	if put.Method != http.MethodPut || put.Bytes != int64(len(body)) || put.Err != nil || put.Host == "" {
		t.Errorf("PUT event = %+v", put)
	}
	if get.Method != http.MethodGet || get.Bytes != int64(len(body)) || get.Err != nil {
		t.Errorf("GET event = %+v", get)
	}
	var ie *paladin.IntegrityError
	if !errors.As(short.Err, &ie) {
		t.Errorf("short GET event err = %v, want an IntegrityError", short.Err)
	}
	var te *paladin.TransferError
	if !errors.As(refused.Err, &te) || te.Status != http.StatusForbidden {
		t.Errorf("refused GET event err = %v, want a 403 TransferError", refused.Err)
	}
	want := []slog.Level{slog.LevelDebug, slog.LevelDebug, slog.LevelWarn, slog.LevelWarn}
	if got := logs.levels(); len(got) != len(want) || got[2] != slog.LevelWarn || got[0] != slog.LevelDebug {
		t.Errorf("logged %v, want %v", got, want)
	}
}

// OpenTelemetry needs nothing from the SDK: connect's own otelconnect
// interceptor and otelhttp's transport plug into the options there are, and
// carry the W3C trace context to the server and to storage.
func TestOpenTelemetryThroughTheExtensionPoints(t *testing.T) {
	spans := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spans))
	propagator := propagation.TraceContext{}
	otelInterceptor, err := otelconnect.NewClientInterceptor(
		otelconnect.WithTracerProvider(provider), otelconnect.WithPropagator(propagator))
	if err != nil {
		t.Fatal(err)
	}
	tr := mustTransfer(t, paladin.WithTransferHTTPClient(&http.Client{
		Transport: otelhttp.NewTransport(http.DefaultTransport,
			otelhttp.WithTracerProvider(provider), otelhttp.WithPropagators(propagator)),
	}))

	// The RPC leg.
	rec := &recorder{}
	health, _ := clients(t, serve(t, rec), paladin.WithInterceptors(otelInterceptor))
	if _, err := health.GetVersion(context.Background(), &iamv1.GetVersionRequest{}); err != nil {
		t.Fatal(err)
	}
	if rec.last().Get("Traceparent") == "" {
		t.Error("the RPC carried no traceparent")
	}

	// The presigned leg.
	data, _, st := newTransfer(t, 0, paladin.WithTransfer(tr))
	obj := upload(t, data, "k", []byte("traced"))
	r, err := paladin.Download(context.Background(), data, obj.GetName(), paladin.DownloadOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, _ = io.Copy(io.Discard, r)
	_ = r.Close()
	if st.traceparents() < 2 {
		t.Errorf("storage saw %d traceparents, want the PUT's and the GET's", st.traceparents())
	}
	if got := len(spans.Ended()); got < 3 {
		t.Errorf("%d spans ended, want the RPC's and both transfers'", got)
	}
}
