package worker

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/admin/v1/admindomain"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

const testSigningSecret = "inline-signing-secret"

type signedDelivery struct {
	signature, legacy string
	body              []byte
}

// signingSink records each delivery's signatures and body, answering the
// statuses given in turn (then 200).
func signingSink(t *testing.T, statuses ...int) (*httptest.Server, func() []signedDelivery) {
	t.Helper()
	var (
		mu  sync.Mutex
		got []signedDelivery
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		mu.Lock()
		n := len(got)
		got = append(got, signedDelivery{
			signature: r.Header.Get(paladin.HeaderWebhookSignature),
			legacy:    r.Header.Get(legacySignatureHeader),
			body:      body,
		})
		mu.Unlock()
		if n < len(statuses) {
			w.WriteHeader(statuses[n])
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)
	return srv, func() []signedDelivery {
		mu.Lock()
		defer mu.Unlock()
		return append([]signedDelivery(nil), got...)
	}
}

func signedSub(t *testing.T, url string) admindomain.EventSubscription {
	t.Helper()
	cfg, err := json.Marshal(map[string]any{"url": url, "signing_secret_ref": testSigningSecret})
	if err != nil {
		t.Fatal(err)
	}
	return admindomain.EventSubscription{
		SubscriptionID: uuid.Must(uuid.NewV7()),
		TenantID:       uuid.Must(uuid.NewV7()),
		SinkKind:       "http",
		SinkConfig:     cfg,
	}
}

// A delivery carries the timestamped signature, which the SDK verifies, and
// for now the body-only one beside it.
func TestDeliverHTTPSignsWithATimestamp(t *testing.T) {
	srv, deliveries := signingSink(t)
	signedAt := time.Unix(1_767_225_600, 0)
	d := &Dispatcher{HTTPClient: srv.Client(), MaxAttempts: 1, Now: func() time.Time { return signedAt }}

	if err := d.DeliverOne(context.Background(), signedSub(t, srv.URL), "paladin.bucket.updated"); err != nil {
		t.Fatalf("DeliverOne: %v", err)
	}
	got := deliveries()
	if len(got) != 1 {
		t.Fatalf("deliveries = %d, want 1", len(got))
	}
	if err := paladin.VerifyWebhook(testSigningSecret, got[0].signature, got[0].body,
		paladin.WithWebhookClock(func() time.Time { return signedAt })); err != nil {
		t.Fatalf("the signature does not verify: %v (header %q)", err, got[0].signature)
	}
	if !strings.HasPrefix(got[0].signature, "t="+strconv.FormatInt(signedAt.Unix(), 10)+",") {
		t.Errorf("signature %q is not stamped with the dispatcher's clock", got[0].signature)
	}
	mac := hmac.New(sha256.New, []byte(testSigningSecret))
	mac.Write(got[0].body)
	if want := legacySignaturePrefix + hex.EncodeToString(mac.Sum(nil)); got[0].legacy != want {
		t.Errorf("legacy signature = %q, want %q", got[0].legacy, want)
	}
}

// A retry is signed again at its own time: with backoff up to an hour, the
// first attempt's timestamp would be stale when the retry lands.
func TestDeliverHTTPSignsEachAttempt(t *testing.T) {
	srv, deliveries := signingSink(t, http.StatusInternalServerError)
	clock := time.Unix(1_767_225_600, 0)
	const step = time.Minute
	d := &Dispatcher{HTTPClient: srv.Client(), MaxAttempts: 2, BaseBackoff: time.Millisecond,
		Now: func() time.Time { clock = clock.Add(step); return clock }}

	if err := d.DeliverOne(context.Background(), signedSub(t, srv.URL), "paladin.bucket.updated"); err != nil {
		t.Fatalf("DeliverOne: %v", err)
	}
	got := deliveries()
	if len(got) != 2 {
		t.Fatalf("deliveries = %d, want 2", len(got))
	}
	if got[0].signature == got[1].signature {
		t.Fatal("the retry reused the first attempt's signature")
	}
	if err := paladin.VerifyWebhook(testSigningSecret, got[1].signature, got[1].body,
		paladin.WithWebhookClock(func() time.Time { return clock })); err != nil {
		t.Fatalf("the retry's signature does not verify at its own time: %v", err)
	}
}

// Without a signing secret nothing is signed.
func TestDeliverHTTPUnsignedWithoutASecret(t *testing.T) {
	srv, deliveries := signingSink(t)
	cfg, _ := json.Marshal(map[string]any{"url": srv.URL})
	sub := admindomain.EventSubscription{
		SubscriptionID: uuid.Must(uuid.NewV7()), TenantID: uuid.Must(uuid.NewV7()),
		SinkKind: "http", SinkConfig: cfg,
	}
	d := &Dispatcher{HTTPClient: srv.Client(), MaxAttempts: 1}
	if err := d.DeliverOne(context.Background(), sub, "paladin.bucket.updated"); err != nil {
		t.Fatalf("DeliverOne: %v", err)
	}
	if got := deliveries(); len(got) != 1 || got[0].signature != "" || got[0].legacy != "" {
		t.Fatalf("deliveries = %+v, want one unsigned", got)
	}
}
