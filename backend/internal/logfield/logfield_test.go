package logfield

import (
	"strings"
	"testing"
)

func TestURLHidesThePassword(t *testing.T) {
	for raw, want := range map[string]string{
		"amqp://user:s3cret@rabbit.svc:5672/vhost": "amqp://user:xxxxx@rabbit.svc:5672/vhost",
		"nats://u:s3cret@nats.svc:4222":            "nats://u:xxxxx@nats.svc:4222",
		"nats://nats.svc:4222":                     "nats://nats.svc:4222",
		"amqp://user@rabbit.svc":                   "amqp://user@rabbit.svc",
		"%%s3cret":                                 unparseableURL,
	} {
		f := URL("url", raw)
		if f.String != want {
			t.Errorf("URL(%q) = %q, want %q", raw, f.String, want)
		}
		if strings.Contains(f.String, "s3cret") {
			t.Errorf("URL(%q) logged the password", raw)
		}
	}
}
