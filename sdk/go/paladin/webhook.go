package paladin

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Webhook deliveries are signed so a subscriber can tell them from forgeries
// and from replays: HeaderWebhookSignature carries t=<unix seconds>,v1=<hex
// HMAC-SHA256 of "<t>.<body>">, keyed by the subscription's signing secret.
// The server signs with SignWebhook; a subscriber checks with VerifyWebhook.
// The format is pinned by sdk/testdata/webhook_signatures.json, which the
// server's and both SDKs' tests read.
//
// A replay inside the tolerance window verifies by design. Deduplicate on the
// delivery's X-Paladin-Event-Id, which is stable across retries.

const (
	// HeaderWebhookSignature carries a delivery's timestamped signature.
	HeaderWebhookSignature = "X-Paladin-Webhook-Signature"
	// DefaultWebhookTolerance is how far a delivery's timestamp may be from
	// the subscriber's clock, either way, and still verify.
	DefaultWebhookTolerance = 5 * time.Minute
)

// The signature header's grammar.
const (
	webhookPairSep    = ","
	webhookKeyValSep  = "="
	webhookSignedSep  = "."
	webhookTimeKey    = "t"
	webhookV1Key      = "v1"
	webhookTimeBase   = 10
	webhookTimeBitLen = 64
)

// ErrWebhookSignature is a delivery that does not verify: malformed, signed
// with another secret, altered, or outside the tolerance window.
var ErrWebhookSignature = errors.New("paladin: invalid webhook signature")

// webhookMAC is the v1 HMAC of body signed at unix second t.
func webhookMAC(secret string, t int64, body []byte) []byte {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(t, webhookTimeBase) + webhookSignedSep))
	mac.Write(body)
	return mac.Sum(nil)
}

func allDigits(s string) bool {
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return s != ""
}

// SignWebhook is the HeaderWebhookSignature value for body, signed at t.
func SignWebhook(secret string, t time.Time, body []byte) string {
	unix := t.Unix()
	return webhookTimeKey + webhookKeyValSep + strconv.FormatInt(unix, webhookTimeBase) +
		webhookPairSep + webhookV1Key + webhookKeyValSep + hex.EncodeToString(webhookMAC(secret, unix, body))
}

type webhookConfig struct {
	tolerance time.Duration
	now       func() time.Time
}

// WebhookOption configures VerifyWebhook.
type WebhookOption func(*webhookConfig)

// WithWebhookTolerance replaces DefaultWebhookTolerance.
func WithWebhookTolerance(d time.Duration) WebhookOption {
	return func(c *webhookConfig) { c.tolerance = d }
}

// WithWebhookClock replaces time.Now, for tests.
func WithWebhookClock(now func() time.Time) WebhookOption {
	return func(c *webhookConfig) { c.now = now }
}

// VerifyWebhook checks a delivery: header is its HeaderWebhookSignature value,
// body its raw bytes as received. It returns nil when one v1 signature matches
// under secret, compared in constant time, and the timestamp is within the
// tolerance of now; otherwise an error wrapping ErrWebhookSignature.
func VerifyWebhook(secret, header string, body []byte, opts ...WebhookOption) error {
	cfg := webhookConfig{tolerance: DefaultWebhookTolerance, now: time.Now}
	for _, o := range opts {
		o(&cfg)
	}
	var (
		t       int64
		hasTime bool
		sigs    [][]byte
	)
	for _, pair := range strings.Split(header, webhookPairSep) {
		key, value, _ := strings.Cut(pair, webhookKeyValSep)
		switch key {
		case webhookTimeKey:
			// Digits only: ParseInt alone would also take a sign.
			parsed, err := strconv.ParseInt(value, webhookTimeBase, webhookTimeBitLen)
			if err != nil || hasTime || !allDigits(value) {
				return fmt.Errorf("%w: bad timestamp", ErrWebhookSignature)
			}
			t, hasTime = parsed, true
		case webhookV1Key:
			// A value that is not hex cannot match; it is skipped, not fatal,
			// so another v1 beside it can still verify.
			if sig, err := hex.DecodeString(value); err == nil {
				sigs = append(sigs, sig)
			}
		}
	}
	if !hasTime || len(sigs) == 0 {
		return fmt.Errorf("%w: want t=<unix seconds>,v1=<hex>", ErrWebhookSignature)
	}
	want := webhookMAC(secret, t, body)
	matched := false
	for _, sig := range sigs {
		if hmac.Equal(sig, want) {
			matched = true
		}
	}
	if !matched {
		return fmt.Errorf("%w: no signature matches", ErrWebhookSignature)
	}
	if skew := cfg.now().Sub(time.Unix(t, 0)).Abs(); skew > cfg.tolerance {
		return fmt.Errorf("%w: signed %s from now, beyond %s", ErrWebhookSignature, skew, cfg.tolerance)
	}
	return nil
}
