package paladin

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
)

// How a presigned request is retried. A failed transfer is tried again with
// a URL presigned afresh, so an attempt never reuses one that expired or was
// refused; a request that cannot succeed by repeating — a 4xx other than an
// expiry — is not retried.
const (
	// DefaultTransferAttempts is how many times one presigned request is
	// sent before its last error is returned.
	DefaultTransferAttempts = 4
	// PresignExpirySkew is how close to its expiry a URL is presigned again
	// instead of sent: room for clock skew and for the request itself.
	PresignExpirySkew = 30 * time.Second
	// transferBackoffBase and transferBackoffMax bound the wait between
	// attempts: base, doubling, at most max.
	transferBackoffBase = 200 * time.Millisecond
	transferBackoffMax  = 5 * time.Second
)

// Statuses a presigned request may be retried after: the store was busy or
// down, not refusing this request.
var retryableStatus = map[int]bool{
	http.StatusRequestTimeout:      true,
	http.StatusTooManyRequests:     true,
	http.StatusInternalServerError: true,
	http.StatusBadGateway:          true,
	http.StatusServiceUnavailable:  true,
	http.StatusGatewayTimeout:      true,
}

// expiredMarker is in what S3-compatible stores answer when a presigned URL,
// or the session credentials that signed it, has expired ("Request has
// expired", "ExpiredToken"): a 403 whose cause a fresh URL removes.
const expiredMarker = "expired"

// Expired reports whether err is storage refusing a presigned URL because it
// expired.
func Expired(err error) bool {
	var te *TransferError
	return errors.As(err, &te) && te.Status == http.StatusForbidden &&
		strings.Contains(strings.ToLower(te.Body), expiredMarker)
}

// AlreadyStored reports whether err is storage refusing an upload because an
// object is already at the key: the URL's If-None-Match: * held. After a PUT
// whose answer was lost, that means the first attempt landed.
func AlreadyStored(err error) bool {
	var te *TransferError
	return errors.As(err, &te) && te.Status == http.StatusPreconditionFailed
}

// retryable reports whether sending the request again, with a fresh URL, may
// succeed: a busy or failing store, an expired URL, or a transport failure
// that is not the caller's context ending.
func retryable(ctx context.Context, err error) bool {
	if err == nil || ctx.Err() != nil {
		return false
	}
	var te *TransferError
	if errors.As(err, &te) {
		return retryableStatus[te.Status] || Expired(err)
	}
	return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
}

// usable reports whether a presigned URL still has PresignExpirySkew left; a
// URL with no expiry is taken as usable.
func usable(signed *commonv1.PresignedUrl, now time.Time) bool {
	raw := signed.GetExpiresAtRfc3339()
	if raw == "" {
		return true
	}
	exp, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return true
	}
	return exp.Sub(now) > PresignExpirySkew
}

// backoff is the wait before attempt n (1-based, after the first failure).
func backoff(n int) time.Duration {
	d := transferBackoffBase << (n - 1)
	if d <= 0 || d > transferBackoffMax {
		return transferBackoffMax
	}
	return d
}

// presignFunc presigns a URL for the request afresh.
type presignFunc func(ctx context.Context) (*commonv1.PresignedUrl, error)

// withRetries sends a presigned request through first — presigning again when
// it is near its expiry — and on a retryable failure waits, presigns a fresh
// URL and sends again, up to attempts times. send must be safe to repeat: it
// rewinds its own body.
func withRetries(ctx context.Context, attempts int, first *commonv1.PresignedUrl, presign presignFunc,
	send func(ctx context.Context, signed *commonv1.PresignedUrl) error,
) error {
	if attempts < 1 {
		attempts = 1
	}
	signed := first
	var err error
	for n := 1; ; n++ {
		if signed == nil || !usable(signed, time.Now()) {
			if signed, err = presign(ctx); err != nil {
				return err
			}
		}
		if err = send(ctx, signed); err == nil || n >= attempts || !retryable(ctx, err) {
			return err
		}
		select {
		case <-time.After(backoff(n)):
		case <-ctx.Done():
			return err
		}
		signed = nil // the next attempt goes through a fresh URL
	}
}
