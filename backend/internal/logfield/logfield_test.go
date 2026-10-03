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

// URL used to hide only the userinfo password. A presigned URL's credential
// is its query — X-Amz-Signature, X-Amz-Credential, X-Amz-Security-Token —
// and logging it handed a working download link to anyone who reads logs.
func TestURLHidesQueryValues(t *testing.T) {
	const presigned = "https://s3.example.com/b/k?X-Amz-Algorithm=AWS4-HMAC-SHA256" +
		"&X-Amz-Credential=ASIAEXAMPLE%2F20261003%2Fus-east-1%2Fs3%2Faws4_request" +
		"&X-Amz-Security-Token=s3cret-token&X-Amz-Signature=s3cretsig#frag-s3cret"
	f := URL("url", presigned)
	for _, leak := range []string{"s3cret", "ASIAEXAMPLE", "AWS4-HMAC-SHA256", "frag"} {
		if strings.Contains(f.String, leak) {
			t.Errorf("URL logged %q: %s", leak, f.String)
		}
	}
	for _, kept := range []string{"https://s3.example.com/b/k", "X-Amz-Signature=REDACTED", "X-Amz-Credential=REDACTED"} {
		if !strings.Contains(f.String, kept) {
			t.Errorf("URL dropped %q: %s", kept, f.String)
		}
	}
}

func TestURLRedactsBothUserinfoAndQuery(t *testing.T) {
	f := URL("url", "https://hook:s3cret@sink.example.com/in?token=s3cret")
	if want := "https://hook:xxxxx@sink.example.com/in?token=REDACTED"; f.String != want {
		t.Fatalf("URL = %q, want %q", f.String, want)
	}
}
