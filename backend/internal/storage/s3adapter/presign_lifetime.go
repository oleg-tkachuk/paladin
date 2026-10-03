package s3adapter

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"time"
)

// SigV4 query parameters that carry a presigned URL's lifetime. The URL is
// the authority on when it expires: the object store measures X-Amz-Expires
// from X-Amz-Date, not from whenever this process got round to asking.
const (
	amzDateParam    = "X-Amz-Date"
	amzExpiresParam = "X-Amz-Expires"
	// amzDateLayout is SigV4's basic ISO-8601 timestamp.
	amzDateLayout = "20060102T150405Z"
)

// minPresignExpiry is the shortest X-Amz-Expires SigV4 accepts.
const minPresignExpiry = time.Second

// ErrCredentialsExpiring is returned when the signing credentials have less
// than minPresignExpiry left: a URL signed with them would be dead on arrival.
var ErrCredentialsExpiring = errors.New("signing credentials expire too soon to presign")

// presignTTL returns the lifetime to sign with: ttl, shortened to what the
// signing credentials have left.
//
// A presigned URL is only as valid as the credentials that signed it. Under
// assume_role, web_identity or an instance role those are session
// credentials, and S3 refuses the URL once the session ends whatever
// X-Amz-Expires says — so signing for a week with a one-hour session hands
// out a URL that claims a week and works for an hour. Shortening it here
// keeps the reported expiry true; the contract already tells clients the
// server may shorten.
func (c *Client) presignTTL(ctx context.Context, ttl time.Duration) (time.Duration, error) {
	creds, err := c.creds.Retrieve(ctx)
	if err != nil {
		return 0, fmt.Errorf("presign credentials: %w", err)
	}
	if !creds.CanExpire {
		return ttl, nil
	}
	left := time.Until(creds.Expires).Truncate(time.Second)
	if left < minPresignExpiry {
		return 0, ErrCredentialsExpiring
	}
	return min(ttl, left), nil
}

// signedURLExpiry reads a presigned URL's expiry from its own query:
// X-Amz-Date plus X-Amz-Expires.
func signedURLExpiry(rawURL string) (time.Time, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse presigned url: %w", err)
	}
	q := u.Query()
	signedAt, err := time.Parse(amzDateLayout, q.Get(amzDateParam))
	if err != nil {
		return time.Time{}, fmt.Errorf("presigned url %s: %w", amzDateParam, err)
	}
	secs, err := strconv.ParseInt(q.Get(amzExpiresParam), 10, 64)
	if err != nil {
		return time.Time{}, fmt.Errorf("presigned url %s: %w", amzExpiresParam, err)
	}
	return signedAt.Add(time.Duration(secs) * time.Second), nil
}

// downloadCacheControl is the Cache-Control a presigned GET asks the object
// store to answer with: private, and fresh for no longer than the URL lives.
func downloadCacheControl(ttl time.Duration) string {
	return "private, max-age=" + strconv.FormatInt(int64(ttl/time.Second), 10)
}
