package worker

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/api/admin/v1/admindomain"
)

// captureHTTPSink stands up a server that records the Content-Type + body of
// the single POST it receives, and returns the Dispatcher + sub wired to it.
func captureHTTPSink(t *testing.T, format string) (*Dispatcher, admindomain.EventSubscription, *string, *[]byte) {
	t.Helper()
	var (
		gotCT   string
		gotBody []byte
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCT = r.Header.Get("Content-Type")
		gotBody, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	cfg, _ := json.Marshal(map[string]any{"url": srv.URL, "format": format})
	d := &Dispatcher{HTTPClient: srv.Client(), MaxAttempts: 1}
	sub := admindomain.EventSubscription{
		SubscriptionID: uuid.Must(uuid.NewV7()),
		TenantID:       uuid.Must(uuid.NewV7()),
		SinkKind:       "http",
		SinkConfig:     cfg,
	}
	return d, sub, &gotCT, &gotBody
}

// TestDeliverHTTP_EmitsEventIdHeaderBothFormats: the dedup key (CloudEvents
// `id`) MUST ride the X-PALADIN-Event-Id header on BOTH formats — the raw body has
// no CloudEvents envelope, so the header is a raw subscriber's only dedup key.
func TestDeliverHTTP_EmitsEventIdHeaderBothFormats(t *testing.T) {
	for _, format := range []string{"cloudevents", "raw"} {
		t.Run(format, func(t *testing.T) {
			var gotID string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotID = r.Header.Get("X-PALADIN-Event-Id")
				w.WriteHeader(http.StatusOK)
			}))
			t.Cleanup(srv.Close)
			cfg, _ := json.Marshal(map[string]any{"url": srv.URL, "format": format})
			d := &Dispatcher{HTTPClient: srv.Client(), MaxAttempts: 1}
			sub := admindomain.EventSubscription{
				SubscriptionID: uuid.Must(uuid.NewV7()),
				TenantID:       uuid.Must(uuid.NewV7()),
				SinkKind:       "http",
				SinkConfig:     cfg,
			}
			if err := d.DeliverOne(context.Background(), sub, "paladin.bucket.updated"); err != nil {
				t.Fatalf("DeliverOne: %v", err)
			}
			// The DeliverOne path has no delivery row, so the id falls back to
			// the subscription id — still stable + present, which is the point.
			if gotID != sub.SubscriptionID.String() {
				t.Errorf("format %s: X-PALADIN-Event-Id = %q, want %q (dedup key must be set)",
					format, gotID, sub.SubscriptionID.String())
			}
		})
	}
}

// TestDeliverHTTP_CloudEventsFormat: format="cloudevents" POSTs the CloudEvents
// 1.0 envelope with Content-Type application/cloudevents+json — symmetric with
// the broker sinks.
func TestDeliverHTTP_CloudEventsFormat(t *testing.T) {
	d, sub, ct, body := captureHTTPSink(t, "cloudevents")
	if err := d.DeliverOne(context.Background(), sub, "paladin.bucket.updated"); err != nil {
		t.Fatalf("DeliverOne: %v", err)
	}
	if *ct != "application/cloudevents+json" {
		t.Errorf("Content-Type = %q, want application/cloudevents+json", *ct)
	}
	var env cloudEventEnvelope
	if err := json.Unmarshal(*body, &env); err != nil {
		t.Fatalf("body is not a CloudEvents envelope: %v", err)
	}
	if env.SpecVersion != "1.0" || env.Type != "paladin.bucket.updated" {
		t.Errorf("envelope mismatch: %+v", env)
	}
}

// TestDeliverHTTP_RawFormatExplicit: format="raw" keeps the legacy bare Event
// JSON (Content-Type application/json) for a pre-flip subscriber that opts back
// in. The raw shape has a top-level "type" but no "specversion".
func TestDeliverHTTP_RawFormatExplicit(t *testing.T) {
	d, sub, ct, body := captureHTTPSink(t, "raw")
	if err := d.DeliverOne(context.Background(), sub, "paladin.bucket.updated"); err != nil {
		t.Fatalf("DeliverOne: %v", err)
	}
	if *ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", *ct)
	}
	var raw map[string]any
	if err := json.Unmarshal(*body, &raw); err != nil {
		t.Fatalf("body not JSON: %v", err)
	}
	if _, hasEnvelope := raw["specversion"]; hasEnvelope {
		t.Error("raw format must NOT be a CloudEvents envelope (no specversion)")
	}
	if raw["type"] != "paladin.bucket.updated" {
		t.Errorf("raw event type = %v, want paladin.bucket.updated", raw["type"])
	}
}

// TestDeliverHTTP_DefaultIsCloudEvents: an empty/absent format now defaults to
// the CloudEvents 1.0 envelope (the flip) — symmetric with the broker sinks.
func TestDeliverHTTP_DefaultIsCloudEvents(t *testing.T) {
	d, sub, ct, body := captureHTTPSink(t, "")
	if err := d.DeliverOne(context.Background(), sub, "paladin.bucket.updated"); err != nil {
		t.Fatalf("DeliverOne: %v", err)
	}
	if *ct != "application/cloudevents+json" {
		t.Errorf("Content-Type = %q, want application/cloudevents+json (default flipped)", *ct)
	}
	var env cloudEventEnvelope
	if err := json.Unmarshal(*body, &env); err != nil {
		t.Fatalf("default body is not a CloudEvents envelope: %v", err)
	}
	if env.SpecVersion != "1.0" || env.Type != "paladin.bucket.updated" {
		t.Errorf("envelope mismatch: %+v", env)
	}
}
