package testimage

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/moby/moby/client"
)

const ref = "example/img:1"

// daemon answers the two calls Ensure makes: inspect from a script of
// presences, pull from a script of stream bodies, one entry per call.
type daemon struct {
	present []bool
	pulls   []string
	inspect int
	pulled  int
}

const (
	pullOK          = `{"status":"Pull complete"}` + "\n"
	pullRateLimited = `{"status":"Pulling"}` + "\n" + `{"errorDetail":{"message":"toomanyrequests: rate limit"},"error":"toomanyrequests: rate limit"}` + "\n"
	notFoundBody    = `{"message":"No such image: example/img:1"}`
)

func (d *daemon) do(r *http.Request) (*http.Response, error) {
	reply := func(code int, body string) *http.Response {
		return &http.Response{StatusCode: code, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(body))}
	}
	switch {
	case strings.HasSuffix(r.URL.Path, "/json"):
		i := min(d.inspect, len(d.present)-1)
		d.inspect++
		if d.present[i] {
			return reply(http.StatusOK, `{"Id":"sha256:abc"}`), nil
		}
		return reply(http.StatusNotFound, notFoundBody), nil
	case strings.HasSuffix(r.URL.Path, "/images/create"):
		i := min(d.pulled, len(d.pulls)-1)
		d.pulled++
		return reply(http.StatusOK, d.pulls[i]), nil
	}
	return reply(http.StatusNotFound, `{"message":"unexpected `+r.URL.Path+`"}`), nil
}

// roundTrip answers every request the client sends from the daemon script.
type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func newClient(t *testing.T, d *daemon) *client.Client {
	t.Helper()
	cli, err := client.New(
		client.WithHost("tcp://docker.invalid:2375"),
		client.WithHTTPClient(&http.Client{Transport: roundTrip(d.do)}),
	)
	if err != nil {
		t.Fatal(err)
	}
	return cli
}

func run(t *testing.T, d *daemon) (int, error) {
	t.Helper()
	cli := newClient(t, d)
	waits := 0
	err := ensure(context.Background(), cli, ref, func(context.Context, time.Duration) error {
		waits++
		return nil
	})
	return waits, err
}

func TestEnsure(t *testing.T) {
	cases := []struct {
		name      string
		d         daemon
		wantErr   string
		wantPulls int
	}{
		{"already there: no pull", daemon{present: []bool{true}, pulls: []string{pullOK}}, "", 0},
		{"missing: pulled, then there", daemon{present: []bool{false, true}, pulls: []string{pullOK}}, "", 1},
		{"a failure inside the stream is retried", daemon{present: []bool{false, true}, pulls: []string{pullRateLimited, pullOK}}, "", 2},
		{"a pull that keeps failing says why", daemon{present: []bool{false}, pulls: []string{pullRateLimited}}, "toomanyrequests", pullAttempts},
		{"a pull that leaves no image fails", daemon{present: []bool{false}, pulls: []string{pullOK}}, "the image is not there", pullAttempts},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := tc.d
			waits, err := run(t, &d)
			if tc.wantErr == "" && err != nil {
				t.Fatalf("Ensure() = %v, want nil", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("Ensure() = %v, want an error naming %q", err, tc.wantErr)
			}
			if d.pulled != tc.wantPulls {
				t.Errorf("pulls = %d, want %d", d.pulled, tc.wantPulls)
			}
			if wantWaits := max(tc.wantPulls-1, 0); waits != wantWaits {
				t.Errorf("waits = %d, want %d", waits, wantWaits)
			}
		})
	}
}

// A context cancelled between attempts stops the retries.
func TestEnsure_StopsWhenCancelled(t *testing.T) {
	d := &daemon{present: []bool{false}, pulls: []string{pullRateLimited}}
	cli := newClient(t, d)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := ensure(ctx, cli, ref, sleep); err == nil {
		t.Fatal("Ensure() = nil, want the cancellation")
	}
	if d.pulled > 1 {
		t.Errorf("pulls = %d after cancellation, want at most 1", d.pulled)
	}
}
