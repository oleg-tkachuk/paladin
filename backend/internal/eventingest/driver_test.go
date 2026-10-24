package eventingest

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// One failing event does not stop the rest of its message: those still run,
// and the message is not acked.
func TestDeliverEach(t *testing.T) {
	boom := errors.New("boom")
	evs := []CloudEvent{{ID: "a"}, {ID: "b"}, {ID: "c"}}
	var seen []string
	err := deliverEach(t.Context(), evs, func(_ context.Context, ev CloudEvent) error {
		seen = append(seen, ev.ID)
		if ev.ID == "b" {
			return boom
		}
		return nil
	})
	if !errors.Is(err, boom) {
		t.Errorf("err = %v, want the failure", err)
	}
	if len(seen) != len(evs) {
		t.Errorf("delivered %v, want every event", seen)
	}
	if err := deliverEach(t.Context(), evs, func(context.Context, CloudEvent) error { return nil }); err != nil {
		t.Errorf("all delivered: err = %v", err)
	}
}

func TestPinMessageID(t *testing.T) {
	cases := []struct {
		name string
		ids  []string
		pin  string
		want []string
	}{
		{"no pinned id keeps the source's", []string{"a", "b"}, "", []string{"a", "b"}},
		{"one event takes the pinned id", []string{"a"}, "m", []string{"m"}},
		{"several keep theirs after it", []string{"a", "b"}, "m", []string{"m" + MessageIDSeparator + "a", "m" + MessageIDSeparator + "b"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			evs := make([]CloudEvent, len(c.ids))
			for i, id := range c.ids {
				evs[i].ID = id
			}
			pinMessageID(evs, c.pin)
			for i := range evs {
				if evs[i].ID != c.want[i] {
					t.Errorf("event %d id = %q, want %q", i, evs[i].ID, c.want[i])
				}
			}
		})
	}
}

// A batched S3 notification over the webhook reaches the handler once per
// record, and a failure of any asks the publisher to send it again.
func TestWebhookHandler_EveryRecord(t *testing.T) {
	src := &S3EventSource{URI: "s3://primary"}
	d := &WebhookDriver{Sources: map[string]Source{"/webhook/s3": src}, MaxBodyBytes: 1 << 16}
	body := []byte(`{"Records":[` + s3RecordJSON("s3:ObjectCreated:Put", "b", "t/ok/one", "r") + `,` +
		s3RecordJSON("s3:ObjectCreated:Put", "b", "t/ok/two", "r") + `]}`)
	post := func(deliver func(context.Context, CloudEvent) error) int {
		r := httptest.NewRecorder()
		d.handler(src, deliver)(r, httptest.NewRequestWithContext(t.Context(), http.MethodPost, "/webhook/s3", bytes.NewReader(body)))
		return r.Code
	}

	var keys []string
	if code := post(func(_ context.Context, ev CloudEvent) error {
		keys = append(keys, ev.SubjectFields.Key)
		return nil
	}); code != http.StatusOK || len(keys) != 2 || keys[0] != "one" || keys[1] != "two" {
		t.Errorf("code %d, delivered %v; want 200 and both records in order", code, keys)
	}
	if code := post(func(_ context.Context, ev CloudEvent) error {
		if ev.SubjectFields.Key == "two" {
			return errors.New("transient")
		}
		return nil
	}); code != http.StatusServiceUnavailable {
		t.Errorf("one record failing: code %d, want 503", code)
	}
}
