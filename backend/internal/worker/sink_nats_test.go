package worker

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
)

// The parts of sink_nats.go that need no broker: the connection-pool key, the
// two identifiers that decide whether a consumer sees an event once or twice,
// and the envelope fields a consumer routes on.
//
// Coverage of this file is spread wider than it looks, which is worth knowing
// before reading a line count as a gap: `parseNatsCredentials` and the
// auth round-trips live in event_dispatcher_test.go, the batch path in
// sink_batch_test.go, and the publish paths in tests/integration
// (jetstream_sink_test.go, dispatcher_test.go) against embedded servers.

func natsSub(t *testing.T, kind, url, subject, creds string) admindomain.EventSubscription {
	t.Helper()
	cfg, err := json.Marshal(natsSinkConfig{
		URL: url, Subject: subject, CredentialsRef: creds,
	})
	if err != nil {
		t.Fatalf("marshal sink config: %v", err)
	}
	return admindomain.EventSubscription{
		SubscriptionID: uuid.New(), SinkKind: kind, SinkConfig: cfg,
	}
}

// The pool key is what stops two subscriptions on the same cluster from
// sharing a connection when they authenticate as different principals.
func TestPoolKey_SeparatesByCredentials(t *testing.T) {
	const url = "nats://broker.internal:4222"

	if poolKey(url, "token:a") == poolKey(url, "token:b") {
		t.Error("two different credentials share a pool key — one tenant's " +
			"events would publish over another principal's connection")
	}
	if poolKey("nats://a:4222", "") == poolKey("nats://b:4222", "") {
		t.Error("two clusters share a pool key")
	}
	if !strings.Contains(poolKey(url, "token:a"), "\x00") {
		t.Error("the separator is gone — a url ending in the credentials " +
			"prefix would collide with a different pair")
	}
}

// natsGroupTarget decides which rows are batched together, and
// deliverNATSBatch resolves the connection from the FIRST item, on the stated
// assumption that "all items share url + credentials_ref by construction".
// That assumption holds only while the grouping key carries the credentials —
// otherwise a batch mixes two auth principals and every row after the first
// publishes under the wrong one.
//
// TestNATSGroupTarget in sink_batch_test.go already covers the url and
// sink-kind cases. These are the three it does not: credentials, a missing
// subject, and a config that does not parse.
func TestNatsGroupTarget_SeparatesByCredentials(t *testing.T) {
	const url = "nats://broker.internal:4222"

	a, okA := natsGroupTarget(natsSub(t, "nats", url, "events", "token:a"))
	b, okB := natsGroupTarget(natsSub(t, "nats", url, "events", "token:b"))
	if !okA || !okB {
		t.Fatal("a well-formed nats subscription was not groupable")
	}
	if a == b {
		t.Error("subscriptions with different credentials landed in one batch; " +
			"the batch dials once, so the second principal's events publish " +
			"over the first's connection")
	}
	if a != poolKey(url, "token:a") {
		t.Errorf("the group key %q is not the pool key — rows would be batched "+
			"by one identity and connected by another", a)
	}
}

func TestNatsGroupTarget_RefusesIncompleteConfigs(t *testing.T) {
	const url = "nats://broker.internal:4222"

	if _, ok := natsGroupTarget(natsSub(t, "nats", url, "", "")); ok {
		t.Error("a subscription with no subject was grouped; there is nowhere " +
			"to publish it")
	}
	unparseable := admindomain.EventSubscription{
		SubscriptionID: uuid.New(), SinkKind: "nats", SinkConfig: []byte("{not json"),
	}
	if _, ok := natsGroupTarget(unparseable); ok {
		t.Error("returned a group key for a config that does not parse")
	}
}

// The comment on natsDedupID states the invariant: the JetStream Nats-Msg-Id
// "matches the id the CloudEvents envelope carries so a consumer's dedup and
// the server's dedup agree". Two functions compute it separately, so they can
// drift, and nothing would fail — the broker would deduplicate on one id while
// consumers deduplicate on another, and a retry would be delivered twice.
func TestNatsDedupIDMatchesTheEnvelopeID(t *testing.T) {
	sub := natsSub(t, "nats", "nats://x:4222", "events", "")
	d := &Dispatcher{}

	t.Run("with a delivery-row id", func(t *testing.T) {
		evt := Event{ID: "row-42", Type: "paladin.object.uploaded", At: time.Now()}
		if got, want := natsDedupID(sub, evt), d.newCloudEventEnvelope(sub, evt).ID; got != want {
			t.Errorf("Nats-Msg-Id %q != CloudEvents id %q", got, want)
		}
		if natsDedupID(sub, evt) != "row-42" {
			t.Error("the delivery-row id was not used; a retry of the same row " +
				"would carry a different id and be delivered again")
		}
	})

	t.Run("without one, on the synchronous test path", func(t *testing.T) {
		evt := Event{Type: "paladin.object.uploaded", At: time.Now()}
		if got, want := natsDedupID(sub, evt), d.newCloudEventEnvelope(sub, evt).ID; got != want {
			t.Errorf("Nats-Msg-Id %q != CloudEvents id %q", got, want)
		}
		if natsDedupID(sub, evt) != sub.SubscriptionID.String() {
			t.Error("the fallback is not the subscription id")
		}
	})
}

// The envelope's `id` is held above, against the JetStream dedup id. These are
// the fields a consumer filters and routes on, and `time` is the one with a
// failure mode that survives review: At is normalised with .UTC() before
// formatting, so an event stamped in a non-UTC zone must not ship its local
// wall clock under a `Z` suffix — or a consumer ordering by time gets an event
// hours out of sequence, with nothing malformed to notice.
func TestCloudEventEnvelope_TimeIsNormalisedToUTC(t *testing.T) {
	sub := natsSub(t, "nats", "nats://x:4222", "events", "")
	at := time.Date(2026, 9, 10, 1, 2, 3, 456789000, time.FixedZone("UTC+3", 3*3600))
	env := (&Dispatcher{}).newCloudEventEnvelope(sub, Event{
		ID: "row-7", Type: "paladin.object.uploaded", At: at,
	})

	if env.Time != "2026-09-09T22:02:03.456789Z" {
		t.Errorf("time = %q, want the same instant in UTC with nanoseconds", env.Time)
	}
	if env.SpecVersion != "1.0" || env.DataContentType != "application/json" {
		t.Errorf("specversion/datacontenttype = %q/%q, want 1.0/application/json",
			env.SpecVersion, env.DataContentType)
	}
	if env.Source != natsDefaultSource {
		t.Errorf("source = %q, want %q", env.Source, natsDefaultSource)
	}
}
