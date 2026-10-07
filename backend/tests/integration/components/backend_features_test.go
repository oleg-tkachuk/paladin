//go:build integration

package components

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/storage/features"
	"github.com/oleg-tkachuk/paladin/backend/internal/storage/s3adapter"
)

// probeDeadline bounds one feature probe against the test container.
const probeDeadline = time.Minute

// s3IdentitiesWithAnonymous adds SeaweedFS' "anonymous" identity, which
// grants its actions to every unsigned request — the configuration the
// compose stack runs with.
const s3IdentitiesWithAnonymous = `{
  "identities": [
    {
      "name": "paladin-test",
      "credentials": [{"accessKey": "paladin-test-access", "secretKey": "paladin-test-secret-key"}],
      "actions": ["Read", "Write", "List", "Admin"]
    },
    {"name": "anonymous", "actions": ["Read"]}
  ]
}`

func probeSeaweed(t *testing.T, identities string) map[features.Feature]features.Result {
	t.Helper()
	ep, ak, sk := startS3With(t, identities)
	c, err := s3adapter.New(context.Background(), config.StorageBackend{
		Region: "us-east-1", Endpoint: ep, ForcePathStyle: true,
		Auth: config.StorageBackendAuth{Mode: config.AuthModeStaticKeys, AccessKey: ak, SecretKey: sk},
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), probeDeadline)
	defer cancel()
	got := map[features.Feature]features.Result{}
	for _, r := range c.ProbeFeatures(ctx) {
		got[r.Feature] = r
	}
	return got
}

// The store the suite and the compose stack run on does every feature
// Paladin uses, when it is configured with signed identities only.
func TestSeaweedFSSupportsEveryFeature(t *testing.T) {
	t.Parallel()
	for f, r := range probeSeaweed(t, s3TestIdentities) {
		if r.Support != features.Supported {
			t.Errorf("%s = %s (%s), want supported", f, r.Support, r.Message)
		}
	}
}

// With SeaweedFS' anonymous identity every unsigned GET is served, so no
// object is private; the probe has to say so rather than report the policy
// supported.
func TestAnAnonymousIdentityFailsTheReadPolicyProbe(t *testing.T) {
	t.Parallel()
	r := probeSeaweed(t, s3IdentitiesWithAnonymous)[features.AnonymousReadPolicy]
	if r.Support != features.Unsupported || !strings.Contains(r.Message, "no policy set") {
		t.Errorf("anonymous_read_policy = %s (%s), want unsupported for serving unsigned requests with no policy",
			r.Support, r.Message)
	}
}
