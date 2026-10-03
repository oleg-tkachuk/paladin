package paladin

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
)

func TestUsable(t *testing.T) {
	now := time.Date(2026, 10, 3, 12, 0, 0, 0, time.UTC)
	for name, tc := range map[string]struct {
		expires string
		want    bool
	}{
		"no expiry":                 {"", true},
		"well inside":               {now.Add(time.Hour).Format(time.RFC3339), true},
		"inside the skew":           {now.Add(PresignExpirySkew - time.Second).Format(time.RFC3339), false},
		"expired":                   {now.Add(-time.Minute).Format(time.RFC3339), false},
		"unparseable is not judged": {"soon", true},
	} {
		if got := usable(&commonv1.PresignedUrl{ExpiresAtRfc3339: tc.expires}, now); got != tc.want {
			t.Errorf("%s: usable = %v, want %v", name, got, tc.want)
		}
	}
}

func TestRetryable(t *testing.T) {
	ctx := context.Background()
	for name, tc := range map[string]struct {
		err  error
		want bool
	}{
		"expired URL":      {&TransferError{Status: http.StatusForbidden, Body: "Request has expired"}, true},
		"access denied":    {&TransferError{Status: http.StatusForbidden, Body: "AccessDenied"}, false},
		"busy":             {&TransferError{Status: http.StatusServiceUnavailable}, true},
		"bad digest":       {&TransferError{Status: http.StatusBadRequest, Body: "BadDigest"}, false},
		"already stored":   {&TransferError{Status: http.StatusPreconditionFailed}, false},
		"connection reset": {errors.New("read: connection reset by peer"), true},
		"caller cancelled": {context.Canceled, false},
	} {
		if got := retryable(ctx, tc.err); got != tc.want {
			t.Errorf("%s: retryable = %v, want %v", name, got, tc.want)
		}
	}
	done, cancel := context.WithCancel(ctx)
	cancel()
	if retryable(done, errors.New("anything")) {
		t.Error("retried after the caller's context ended")
	}
}

func TestBackoffIsBounded(t *testing.T) {
	if backoff(1) != transferBackoffBase || backoff(2) != 2*transferBackoffBase {
		t.Fatalf("backoff(1..2) = %v, %v", backoff(1), backoff(2))
	}
	if backoff(64) != transferBackoffMax {
		t.Fatalf("backoff(64) = %v, want the cap", backoff(64))
	}
}

// withRetries presigns afresh for every attempt after the first, and for a
// first URL already inside the skew.
func TestWithRetriesPresignsAfresh(t *testing.T) {
	ctx := context.Background()
	presigned := 0
	presign := func(context.Context) (*commonv1.PresignedUrl, error) {
		presigned++
		return &commonv1.PresignedUrl{Url: "fresh"}, nil
	}
	var sentTo []string
	failures := 2
	err := withRetries(ctx, 4, &commonv1.PresignedUrl{Url: "first"}, presign, func(_ context.Context, s *commonv1.PresignedUrl) error {
		sentTo = append(sentTo, s.GetUrl())
		if failures > 0 {
			failures--
			return &TransferError{Status: http.StatusServiceUnavailable}
		}
		return nil
	})
	if err != nil || presigned != 2 || len(sentTo) != 3 || sentTo[0] != "first" || sentTo[2] != "fresh" {
		t.Fatalf("err=%v presigned=%d sent=%v", err, presigned, sentTo)
	}

	presigned = 0
	stale := &commonv1.PresignedUrl{Url: "stale", ExpiresAtRfc3339: time.Now().Add(time.Second).Format(time.RFC3339)}
	_ = withRetries(ctx, 1, stale, presign, func(_ context.Context, s *commonv1.PresignedUrl) error {
		if s.GetUrl() == "stale" {
			t.Error("sent a URL inside the expiry skew")
		}
		return nil
	})
	if presigned != 1 {
		t.Fatalf("presigned %d times for a stale first URL, want 1", presigned)
	}
}
