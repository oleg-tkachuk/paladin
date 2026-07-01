package worker

import (
	"strings"
	"testing"

	"go.uber.org/zap"
)

// Statuses + Warmup back the dispatcher's /system/health.json "rabbitmq"
// subsystem check.

func TestRabbitMQConnPool_StatusesEmpty(t *testing.T) {
	p := NewRabbitMQConnPool(zap.NewNop())
	if got := p.Statuses(); len(got) != 0 {
		t.Errorf("fresh pool Statuses() = %v, want empty (healthy-but-empty)", got)
	}
}

func TestRabbitMQConnPool_WarmupAndStatuses(t *testing.T) {
	p := NewRabbitMQConnPool(zap.NewNop())
	// Health is decided per-URL: brokers with "bad" in the URL dial to an
	// unhealthy connection (the dispatcher used them, then the conn dropped).
	p.newPub = func(url string) (rabbitPublisher, error) {
		return &fakeRabbit{isHealthy: !strings.Contains(url, "bad")}, nil
	}

	p.Warmup([]string{"amqp://ok-1", "amqp://bad-1", "amqp://ok-2"})

	st := p.Statuses()
	if len(st) != 3 {
		t.Fatalf("Statuses() = %v, want 3 dialed brokers", st)
	}
	if !st["amqp://ok-1"] || !st["amqp://ok-2"] {
		t.Errorf("healthy brokers reported unhealthy: %v", st)
	}
	if st["amqp://bad-1"] {
		t.Error("a dropped connection should report unhealthy")
	}
}

func TestRabbitMQConnPool_WarmupDialErrorNotCached(t *testing.T) {
	p := NewRabbitMQConnPool(zap.NewNop())
	p.newPub = func(url string) (rabbitPublisher, error) {
		if strings.Contains(url, "unreachable") {
			return nil, errDialFail
		}
		return &fakeRabbit{isHealthy: true}, nil
	}

	p.Warmup([]string{"amqp://reachable", "amqp://unreachable"})

	st := p.Statuses()
	// A broker that never dialed isn't in the pool — the probe only reports
	// on connections the dispatcher actually holds.
	if _, ok := st["amqp://unreachable"]; ok {
		t.Error("a failed dial must not be cached / reported")
	}
	if !st["amqp://reachable"] {
		t.Errorf("reachable broker missing/unhealthy: %v", st)
	}
}

var errDialFail = &dialErr{}

type dialErr struct{}

func (*dialErr) Error() string { return "dial refused" }
