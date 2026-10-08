package worker

import (
	"crypto/tls"
	"strings"
	"testing"

	"go.uber.org/zap"
)

// Conns + Warmup back the dispatcher's /system/health.json "rabbitmq" check.

func TestRabbitMQConnPool_ConnsEmpty(t *testing.T) {
	p := NewRabbitMQConnPool(zap.NewNop())
	if got := p.Conns(); len(got) != 0 {
		t.Errorf("fresh pool Conns() = %v, want empty", got)
	}
}

func TestRabbitMQConnPool_WarmupAndConns(t *testing.T) {
	p := NewRabbitMQConnPool(zap.NewNop())
	// Health is decided per-URL: brokers with "bad" in the URL dial to an
	// unhealthy connection (the dispatcher used them, then the conn dropped).
	p.newPub = func(url string, _ *tls.Config) (rabbitPublisher, error) {
		return &fakeRabbit{isHealthy: !strings.Contains(url, "bad")}, nil
	}

	p.Warmup([]string{"amqp://ok-1", "amqp://bad-1", "amqp://ok-2"})

	got := conns(p.Conns())
	if len(got) != 3 {
		t.Fatalf("Conns() = %v, want 3 dialed brokers", got)
	}
	if got["amqp://ok-1"] != nil || got["amqp://ok-2"] != nil {
		t.Errorf("healthy brokers reported failing: %v", got)
	}
	if got["amqp://bad-1"] == nil {
		t.Error("a dropped connection should report failing")
	}
}

// A broker that refused the dial was left out of the pool, and the probe,
// seeing only the connections it held, reported healthy. The failure is now
// reported, with the password redacted, until a dial succeeds.
func TestRabbitMQConnPool_ReportsAFailedDialUntilOneSucceeds(t *testing.T) {
	p := NewRabbitMQConnPool(zap.NewNop())
	reachable := false
	p.newPub = func(url string, _ *tls.Config) (rabbitPublisher, error) {
		if strings.Contains(url, "flaky") && !reachable {
			return nil, errDialFail
		}
		return &fakeRabbit{isHealthy: true}, nil
	}
	const flaky = "amqp://user:s3cret@flaky:5672"

	p.Warmup([]string{"amqp://reachable", flaky})
	got := conns(p.Conns())
	const redacted = "amqp://user:xxxxx@flaky:5672"
	if got[redacted] == nil || got["amqp://reachable"] != nil {
		t.Fatalf("Conns() = %v, want the failed dial reported, redacted", got)
	}

	reachable = true
	p.Warmup([]string{flaky})
	if got := conns(p.Conns()); got[redacted] != nil || len(got) != 2 {
		t.Errorf("Conns() = %v, want the failure gone once a dial succeeds", got)
	}
}

func TestRabbitWarmupURL(t *testing.T) {
	cases := []struct {
		name, raw, want string
		ok              bool
	}{
		{"url auth", `{"url":"amqp://u:p@h/v","exchange":"e"}`, "amqp://u:p@h/v", true},
		// Each of these dials differently at delivery than the URL alone.
		{"secret-ref url", `{"url":"k8s:rabbit/url","exchange":"e"}`, "", false},
		{"client cert", `{"url":"amqps://h","tls_client_cert":"PEM","tls_client_key":"PEM"}`, "", false},
		{"private ca", `{"url":"amqps://h","tls_ca_cert":"PEM"}`, "", false},
		{"no url", `{"exchange":"e"}`, "", false},
		{"not json", `{`, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := RabbitWarmupURL([]byte(tc.raw))
			if got != tc.want || ok != tc.ok {
				t.Errorf("RabbitWarmupURL = %q, %v; want %q, %v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

// conns indexes Conns by URL.
func conns(cs []BrokerConn) map[string]error {
	out := make(map[string]error, len(cs))
	for _, c := range cs {
		out[c.URL] = c.Err
	}
	return out
}

var errDialFail = &dialErr{}

type dialErr struct{}

func (*dialErr) Error() string { return "dial refused" }
