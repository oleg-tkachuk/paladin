package paladin

import (
	"context"
	"runtime/debug"
	"testing"

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
