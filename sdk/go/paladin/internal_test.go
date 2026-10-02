package paladin

import (
	"context"
	"net/http"
	"runtime/debug"
	"testing"
	"time"

	"connectrpc.com/connect"

	datav1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/data/v1"
)

func TestIdempotencyKeyFor(t *testing.T) {
	const (
		ctxKey  = "from-context"
		bodyKey = "from-body"
	)
	sideEffects := connect.NewRequest(&datav1.UploadObjectRequest{})
	withBody := connect.NewRequest(&datav1.UploadObjectRequest{IdempotencyKey: bodyKey})

	if key, ok := idempotencyKeyFor(WithIdempotencyKey(context.Background(), ctxKey), withBody); !ok || key != ctxKey {
		t.Errorf("context key: got %q, %v; the caller's key wins", key, ok)
	}
	if key, ok := idempotencyKeyFor(context.Background(), withBody); !ok || key != bodyKey {
		t.Errorf("body key: got %q, %v; the server refuses a header that disagrees with the field", key, ok)
	}
	if key, ok := idempotencyKeyFor(context.Background(), sideEffects); !ok || key == "" {
		t.Errorf("no key: got %q, %v; a call with side effects needs one", key, ok)
	}
}

func TestModuleVersion(t *testing.T) {
	const released = "v0.12.0"
	cases := []struct {
		name string
		info *debug.BuildInfo
		ok   bool
		want string
	}{
		{"a versioned dependency", &debug.BuildInfo{Deps: []*debug.Module{{Path: modulePath, Version: released}}}, true, released},
		{"replaced", &debug.BuildInfo{Deps: []*debug.Module{{Path: modulePath, Version: released, Replace: &debug.Module{Path: "../sdk"}}}}, true, develVersion},
		{"not a dependency", &debug.BuildInfo{}, true, develVersion},
		{"no build info", nil, false, develVersion},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := moduleVersion(func() (*debug.BuildInfo, bool) { return tc.info, tc.ok })
			if got != tc.want {
				t.Errorf("moduleVersion = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		value string
		want  time.Duration
		ok    bool
	}{
		{"120", 2 * time.Minute, true},
		{"0", 0, true},
		{"-5", 0, false},
		{now.Add(90 * time.Second).Format(http.TimeFormat), 90 * time.Second, true},
		{"Fri, 02 Oct 2026 12:01:30 GMT", 90 * time.Second, true},
		{"Friday, 02-Oct-26 12:01:30 GMT", 90 * time.Second, true}, // RFC 850, which RFC 9110 still accepts
		{now.Add(-time.Hour).Format(http.TimeFormat), 0, true},     // a date past: retry now
		{"soon", 0, false},
		{"", 0, false},
	}
	for _, tc := range cases {
		got, ok := parseRetryAfter(tc.value, now)
		if got != tc.want || ok != tc.ok {
			t.Errorf("parseRetryAfter(%q) = %v, %v; want %v, %v", tc.value, got, ok, tc.want, tc.ok)
		}
	}
}
