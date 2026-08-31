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

	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/storage/s3adapter"
)

type target struct {
	provider string
	bucket   string
	// ownsBucket: the suite created it and must remove it.
	ownsBucket bool
	client     *s3adapter.Client
	tenant     uuid.UUID
	collection string
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
	endpoint := os.Getenv("PALADIN_CONFORMANCE_ENDPOINT")
	if endpoint == "" {
		t.Skip("PALADIN_CONFORMANCE_ENDPOINT not set — nothing to conform to")
	}
	region := os.Getenv("PALADIN_CONFORMANCE_REGION")
	if region == "" {
		region = "us-east-1"
	}
	provider := os.Getenv("PALADIN_CONFORMANCE_PROVIDER")
	if provider == "" {
		provider = "unknown"
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
		ForcePathStyle: true,
		Auth: config.StorageBackendAuth{
			Mode:      "static_keys",
			AccessKey: os.Getenv("PALADIN_CONFORMANCE_ACCESS_KEY"),
			SecretKey: os.Getenv("PALADIN_CONFORMANCE_SECRET_KEY"),
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
		tenant: uuid.New(), collection: "conf",
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
	fmt.Fprintf(&b, "\n─── capability profile: %s (%s) ───\n",
		tg.provider, os.Getenv("PALADIN_CONFORMANCE_ENDPOINT"))
	for _, c := range tg.caps {
		fmt.Fprintf(&b, "  %-26s %-8s %s\n", c.name, c.status, c.detail)
	}
	t.Log(b.String())
}

func ttl() time.Duration { return 10 * time.Minute }
