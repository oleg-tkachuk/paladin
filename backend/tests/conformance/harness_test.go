//go:build conformance

// Package conformance answers one question about a storage backend: can
// Paladin run on it, and which of the optional things does it do?
//
// It exists because the answer was previously scattered across error paths, a
// comment ("SeaweedFS / real S3: no Sequencer from HEAD; leave empty"), a spec
// document (Garage has no S3-level versioning or tagging), and a guard added
// after a live incident (a bodiless 404 that could not be told from a missing
// bucket). Each of those is a measurement someone made once and wrote down
// somewhere else. This runs them.
package conformance

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/storage/s3adapter"
)

type target struct {
	provider string
	bucket   string
	// ownsBucket: the suite created it and must remove it.
	ownsBucket bool
	client     *s3adapter.Client
	tenant     uuid.UUID
	collection string
	region     string
	caps       []capability
}

// capability records one optional behaviour: supported, not supported, or not
// determined (the probe itself could not run). "unknown" is kept distinct from
// "no" on purpose — a probe that failed for an unrelated reason must not be
// read as a missing feature.
type capability struct {
	name   string
	status string // "yes" | "no" | "unknown"
	detail string
}

// Records live on the target, not in a package variable: two tests build
// their own target, and a shared slice made the second one report the first
// one's findings as well.
func (tg *target) record(name, status, detail string) {
	tg.caps = append(tg.caps, capability{name: name, status: status, detail: detail})
}

func newTarget(t *testing.T) *target {
	t.Helper()
	// Endpoint is how you point at a self-hosted backend. Against real AWS you
	// set none: the SDK resolves the regional endpoint itself, and supplying
	// one would pin the suite to a host AWS may move. So the target is "an
	// endpoint OR an explicit provider", and only the absence of both is a
	// skip.
	endpoint := os.Getenv("PALADIN_CONFORMANCE_ENDPOINT")
	providerEnv := os.Getenv("PALADIN_CONFORMANCE_PROVIDER")
	if endpoint == "" && providerEnv == "" {
		t.Skip("neither PALADIN_CONFORMANCE_ENDPOINT nor PALADIN_CONFORMANCE_PROVIDER set — nothing to conform to")
	}
	region := os.Getenv("PALADIN_CONFORMANCE_REGION")
	if region == "" {
		region = "us-east-1"
	}
	provider := providerEnv
	if provider == "" {
		provider = "unknown"
	}

	// Path style is right for every self-hosted backend here and wrong for
	// AWS, which serves virtual-hosted URLs and has been retiring the other
	// form for years. Defaulting off when no endpoint is set makes the AWS
	// case work without a flag; PALADIN_CONFORMANCE_PATH_STYLE overrides
	// either way, for a backend that wants the opposite of its default.
	pathStyle := endpoint != ""
	if v := os.Getenv("PALADIN_CONFORMANCE_PATH_STYLE"); v != "" {
		pathStyle = v == "1" || strings.EqualFold(v, "true")
	}

	// Static keys when the environment supplies them, the SDK's own chain
	// otherwise — which is how anyone reaches AWS from a laptop (profile,
	// SSO, instance role) and the one mode this suite could not express.
	authMode := "default_chain"
	access := os.Getenv("PALADIN_CONFORMANCE_ACCESS_KEY")
	secret := os.Getenv("PALADIN_CONFORMANCE_SECRET_KEY")
	if access != "" && secret != "" {
		authMode = "static_keys"
	}
	if v := os.Getenv("PALADIN_CONFORMANCE_AUTH_MODE"); v != "" {
		authMode = v
	}
	bucket := os.Getenv("PALADIN_CONFORMANCE_BUCKET")
	owns := bucket == ""
	if owns {
		bucket = "paladin-conf-" + uuid.NewString()[:8]
	}

	backend := config.StorageBackend{
		Kind:           "s3-compatible",
		Provider:       provider,
		Bucket:         bucket,
		Region:         region,
		Endpoint:       endpoint,
		ForcePathStyle: pathStyle,
		Auth: config.StorageBackendAuth{
			Mode:      authMode,
			AccessKey: access,
			SecretKey: secret,
		},
		PartSizeRaw: "8MB",
	}

	ctx := context.Background()
	c, err := s3adapter.New(ctx, backend)
	if err != nil {
		t.Fatalf("build adapter for %s: %v", provider, err)
	}
	tg := &target{
		provider: provider, bucket: bucket, ownsBucket: owns, client: c,
		tenant: uuid.New(), collection: "conf", region: region,
	}
	if owns {
		if err := c.CreateBucket(ctx, "", bucket, region); err != nil {
			t.Fatalf("REQUIRED CreateBucket: %v", err)
		}
		t.Cleanup(func() {
			// Best effort: a backend that refuses the delete leaves an empty
			// bucket behind, which is visible and harmless. Say so rather
			// than failing a run that already reported its results.
			if err := c.DeleteBucket(context.Background(), "", bucket); err != nil {
				t.Logf("cleanup: bucket %q not removed: %v", bucket, err)
			}
		})
	}
	return tg
}

// report prints the capability profile. It is the artefact: the point of the
// suite is not a green tick, it is this table.
func reportProfile(t *testing.T, tg *target) {
	t.Helper()
	var b strings.Builder
	where := os.Getenv("PALADIN_CONFORMANCE_ENDPOINT")
	if where == "" {
		where = "SDK-resolved endpoint, region " + tg.region
	}
	fmt.Fprintf(&b, "\n─── capability profile: %s (%s) ───\n", tg.provider, where)
	for _, c := range tg.caps {
		fmt.Fprintf(&b, "  %-26s %-8s %s\n", c.name, c.status, c.detail)
	}
	t.Log(b.String())
}

func ttl() time.Duration { return 10 * time.Minute }
