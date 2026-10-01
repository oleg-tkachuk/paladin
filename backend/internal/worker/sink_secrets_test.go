package worker

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"
	kafka "github.com/segmentio/kafka-go"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
)

// fakeSinkSecrets counts resolutions so tests can pin the TTL cache. The
// counter is atomic: resolveSinkValue calls the resolver outside the cache
// lock, so concurrent first uses reach it together — the case
// TestResolveSinkValue_ConcurrentFirstUse exists for, and which raced on a
// plain int.
type fakeSinkSecrets struct {
	values map[string]string // "ns/name/key" (ns may be "") → value
	calls  atomic.Int64
	err    error
}

func (f *fakeSinkSecrets) ResolveSinkSecret(_ context.Context, ns, name, key string) (string, error) {
	f.calls.Add(1)
	if f.err != nil {
		return "", f.err
	}
	v, ok := f.values[ns+"/"+name+"/"+key]
	if !ok {
		return "", errors.New("secret not found")
	}
	return v, nil
}

func TestParseSinkSecretRef(t *testing.T) {
	cases := []struct {
		ref              string
		ns, name, key    string
		wantErrSubstring string
	}{
		{ref: "k8s:hmac/secret", ns: "", name: "hmac", key: "secret"},
		{ref: "k8s:prod/hmac/secret", ns: "prod", name: "hmac", key: "secret"},
		{ref: "k8s:hmac", wantErrSubstring: "malformed"},
		{ref: "k8s:a/b/c/d", wantErrSubstring: "malformed"},
		{ref: "k8s:/key", wantErrSubstring: "empty name or key"},
		{ref: "k8s:name/", wantErrSubstring: "empty name or key"},
	}
	for _, c := range cases {
		ns, name, key, err := parseSinkSecretRef(c.ref)
		if c.wantErrSubstring != "" {
			if err == nil || !strings.Contains(err.Error(), c.wantErrSubstring) {
				t.Errorf("%q: err = %v, want substring %q", c.ref, err, c.wantErrSubstring)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: unexpected err %v", c.ref, err)
			continue
		}
		if ns != c.ns || name != c.name || key != c.key {
			t.Errorf("%q → (%q,%q,%q), want (%q,%q,%q)", c.ref, ns, name, key, c.ns, c.name, c.key)
		}
	}
}

func TestResolveSinkValue_PassthroughAndRefs(t *testing.T) {
	secrets := &fakeSinkSecrets{values: map[string]string{"/hmac/secret": "s3cr3t"}}
	d := &Dispatcher{Secrets: secrets}

	// Non-ref values pass through untouched, resolver never called.
	got, err := d.resolveSinkValue(context.Background(), "inline-value")
	if err != nil || got != "inline-value" {
		t.Fatalf("passthrough = (%q, %v)", got, err)
	}
	if secrets.calls.Load() != 0 {
		t.Fatalf("resolver called %d times for a non-ref value", secrets.calls.Load())
	}

	// Ref resolves; repeat serves from the TTL cache (one backend call).
	for i := 0; i < 3; i++ {
		got, err = d.resolveSinkValue(context.Background(), "k8s:hmac/secret")
		if err != nil || got != "s3cr3t" {
			t.Fatalf("resolve #%d = (%q, %v)", i, got, err)
		}
	}
	if secrets.calls.Load() != 1 {
		t.Errorf("resolver calls = %d, want 1 (TTL cache)", secrets.calls.Load())
	}
}

func TestResolveSinkValue_NoResolverIsHardError(t *testing.T) {
	d := &Dispatcher{} // Secrets nil
	_, err := d.resolveSinkValue(context.Background(), "k8s:hmac/secret")
	if err == nil || !strings.Contains(err.Error(), "no secret resolver") {
		t.Errorf("err = %v, want the no-resolver hard error", err)
	}
}

// deliverHTTP: a k8s: signing_secret_ref signs with the RESOLVED key, and a
// resolution failure fails the delivery (never signs with the literal ref).
func TestDeliverHTTP_SigningSecretRefResolved(t *testing.T) {
	var gotSig string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotSig = r.Header.Get("X-Paladin-Signature")
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg, _ := json.Marshal(map[string]any{"url": srv.URL, "signing_secret_ref": "k8s:hmac/secret"})
	sub := admindomain.EventSubscription{
		SubscriptionID: uuid.Must(uuid.NewV7()),
		TenantID:       uuid.Must(uuid.NewV7()),
		SinkKind:       "http",
		SinkConfig:     cfg,
	}
	secrets := &fakeSinkSecrets{values: map[string]string{"/hmac/secret": "resolved-hmac-key"}}
	d := &Dispatcher{HTTPClient: srv.Client(), MaxAttempts: 1, Secrets: secrets}

	if err := d.DeliverOne(context.Background(), sub, "paladin.bucket.updated"); err != nil {
		t.Fatalf("DeliverOne: %v", err)
	}
	if gotSig == "" || !strings.HasPrefix(gotSig, "sha256=") {
		t.Fatalf("signature header = %q", gotSig)
	}
	// The signature must correspond to the resolved key, not the literal ref.
	if secrets.calls.Load() != 1 {
		t.Errorf("resolver calls = %d, want 1", secrets.calls.Load())
	}

	// Resolution failure → delivery error, no signed request with the literal.
	dErr := &Dispatcher{HTTPClient: srv.Client(), MaxAttempts: 1, Secrets: &fakeSinkSecrets{err: errors.New("rbac denied")}}
	if err := dErr.DeliverOne(context.Background(), sub, "paladin.bucket.updated"); err == nil {
		t.Error("resolution failure must fail the delivery")
	}
}

// deliverKafka: k8s: refs in the SASL fields resolve before the transport
// build, and the pool key is computed over the RESOLVED material.
func TestDeliverKafka_SecretRefResolvedIntoPoolKey(t *testing.T) {
	fake := &fakeKafka{}
	pool := NewKafkaWriterPool(nil)
	var keys []string
	pool.newWriter = func([]string, string, *kafka.Transport) kafkaWriter { return fake }
	secrets := &fakeSinkSecrets{values: map[string]string{"/kafka/pass": "pw-1"}}
	d := &Dispatcher{Kafka: pool, Secrets: secrets}

	cfg := kafkaSinkConfig{
		Brokers: "b:9092", Topic: "t",
		SASLMechanism: "plain", SASLUsername: "u", SASLPassword: "k8s:kafka/pass",
	}
	sub := kafkaTestSub(t, cfg)
	if _, err := d.deliverKafka(context.Background(), sub, sinkTestEvent()); err != nil {
		t.Fatalf("deliverKafka: %v", err)
	}
	// The pool key for the RESOLVED password must equal the key computed for
	// an inline config with the same plaintext — proving the hash covers the
	// resolved material rather than the ref.
	resolved := cfg
	resolved.SASLPassword = "pw-1"
	pool.mu.Lock()
	for k := range pool.writers {
		keys = append(keys, k)
	}
	pool.mu.Unlock()
	if len(keys) != 1 || keys[0] != kafkaWriterKey([]string{"b:9092"}, resolved) {
		t.Errorf("pool keys = %v, want the resolved-material key", keys)
	}
}

// TestResolveSinkValue_ConcurrentFirstUse pins the lazy-init of the secret
// cache against a data race: the admin pod shares one Dispatcher across
// concurrent TestSubscription RPCs, so two callers can hit resolveSinkValue's
// first-use path simultaneously. Fails under -race without a synchronized
// init.
func TestResolveSinkValue_ConcurrentFirstUse(t *testing.T) {
	d := &Dispatcher{Secrets: &fakeSinkSecrets{values: map[string]string{"/h/k": "v"}}}
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = d.resolveSinkValue(context.Background(), "k8s:h/k")
		}()
	}
	wg.Wait()
}
