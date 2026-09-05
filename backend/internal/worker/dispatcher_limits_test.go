package worker

import (
	"encoding/json"
	"testing"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
)

// The retry budget decides whether a failing subscription is retried or
// dead-lettered, and every normalisation in it was unheld.
//
// Mutation testing found it: `if sink.MaxAttempts <= 0` flipped to `< 0` leaves
// a configured zero as zero, and downstream `permanent := attempts+1 >= max`
// is then true on the FIRST attempt — every event for that subscription is
// dead-lettered immediately, silently, on a config value an operator can set
// by leaving a field out. The package's own suite stayed green.
func TestMaxAttemptsNormalisation(t *testing.T) {
	sub := func(kind string, maxAttempts any) admindomain.EventSubscription {
		cfg, _ := json.Marshal(map[string]any{"max_attempts": maxAttempts})
		return admindomain.EventSubscription{SinkKind: kind, SinkConfig: cfg}
	}

	cases := []struct {
		name   string
		runner OutboxRunner
		sub    admindomain.EventSubscription
		want   int
		why    string
	}{
		{"unset default falls back to 5", OutboxRunner{}, sub("http", 3), 3,
			"a per-subscription value wins over the default"},
		{"zero in the config is not zero attempts", OutboxRunner{DefaultMaxAttempts: 7},
			sub("http", 0), 7,
			"a missing or zero max_attempts must fall back, not dead-letter everything"},
		{"negative in the config falls back too", OutboxRunner{DefaultMaxAttempts: 7},
			sub("http", -1), 7, "same reasoning as zero"},
		{"zero default falls back to the built-in", OutboxRunner{DefaultMaxAttempts: 0},
			sub("http", 0), 5,
			"both levels unset must still produce a budget"},
		{"a non-http sink ignores the sink config", OutboxRunner{DefaultMaxAttempts: 7},
			sub("kafka", 2), 7,
			"max_attempts is an http-sink field; another kind must not read it"},
		{"unparseable config falls back", OutboxRunner{DefaultMaxAttempts: 7},
			admindomain.EventSubscription{SinkKind: "http", SinkConfig: []byte("not json")}, 7,
			"a broken config must not become a zero budget"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.runner.maxAttemptsFor(c.sub); got != c.want {
				t.Errorf("maxAttemptsFor = %d, want %d — %s", got, c.want, c.why)
			}
		})
	}
}

// NOT TESTED HERE, deliberately: the `attempts+1 >= max` boundary in
// deliverAndMark. A test asserting `attempts+1 >= max` against a hand-written
// table would re-implement the expression and pass no matter what the code
// says — which is the shape this file exists to catch, and it was written that
// way once before being deleted. Reaching the real boundary needs the delivery
// loop and a transaction; it belongs in internal/integration and is recorded
// in BACKLOG.
