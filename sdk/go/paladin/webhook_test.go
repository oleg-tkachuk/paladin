package paladin_test

import (
	"encoding/json"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// sharedWebhookVectors is sdk/testdata/webhook_signatures.json, which the
// Python SDK's tests and the server's read too.
const sharedWebhookVectors = "../../testdata/webhook_signatures.json"

type webhookVectors struct {
	Secret           string `json:"secret"`
	Body             string `json:"body"`
	Timestamp        int64  `json:"timestamp"`
	Header           string `json:"header"`
	ToleranceSeconds int64  `json:"tolerance_seconds"`
	Cases            []struct {
		Name   string `json:"name"`
		Header string `json:"header"`
		Body   string `json:"body"`
		Now    int64  `json:"now"`
		OK     bool   `json:"ok"`
	} `json:"cases"`
}

func loadWebhookVectors(t *testing.T) webhookVectors {
	t.Helper()
	raw, err := os.ReadFile(sharedWebhookVectors)
	if err != nil {
		t.Fatal(err)
	}
	var v webhookVectors
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	return v
}

func TestSignWebhookMatchesTheSharedVector(t *testing.T) {
	v := loadWebhookVectors(t)
	if got := paladin.SignWebhook(v.Secret, time.Unix(v.Timestamp, 0), []byte(v.Body)); got != v.Header {
		t.Fatalf("SignWebhook = %q, want %q", got, v.Header)
	}
}

func TestDefaultWebhookToleranceIsTheVectors(t *testing.T) {
	v := loadWebhookVectors(t)
	if want := time.Duration(v.ToleranceSeconds) * time.Second; paladin.DefaultWebhookTolerance != want {
		t.Fatalf("DefaultWebhookTolerance = %s, the vectors say %s", paladin.DefaultWebhookTolerance, want)
	}
}

func TestVerifyWebhookSharedCases(t *testing.T) {
	v := loadWebhookVectors(t)
	for _, c := range v.Cases {
		t.Run(c.Name, func(t *testing.T) {
			now := time.Unix(c.Now, 0)
			err := paladin.VerifyWebhook(v.Secret, c.Header, []byte(c.Body),
				paladin.WithWebhookClock(func() time.Time { return now }))
			if c.OK && err != nil {
				t.Fatalf("refused: %v", err)
			}
			if !c.OK && !errors.Is(err, paladin.ErrWebhookSignature) {
				t.Fatalf("err = %v, want ErrWebhookSignature", err)
			}
		})
	}
}

// A delivery signed now verifies now, and a narrower window refuses one
// signed a little earlier.
func TestVerifyWebhookRoundTripAndTolerance(t *testing.T) {
	const secret = "s"
	body := []byte(`{"id":"1"}`)
	signed := time.Now()
	header := paladin.SignWebhook(secret, signed, body)
	if err := paladin.VerifyWebhook(secret, header, body); err != nil {
		t.Fatalf("fresh delivery: %v", err)
	}
	later := func() time.Time { return signed.Add(2 * time.Second) }
	if err := paladin.VerifyWebhook(secret, header, body,
		paladin.WithWebhookClock(later), paladin.WithWebhookTolerance(time.Second)); !errors.Is(err, paladin.ErrWebhookSignature) {
		t.Fatalf("outside a 1s window: err = %v", err)
	}
}
