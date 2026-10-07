//go:build integration

package components

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"

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

// The policy the reconciler sets on a public bucket opens that bucket, and
// nothing beside it, to unsigned GETs (ADR-0027).
func TestPublicBucketPolicyOnSeaweedFS(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ep, ak, sk := startS3(t)
	backend := config.StorageBackend{
		Region: "us-east-1", Endpoint: ep, ForcePathStyle: true,
		Auth: config.StorageBackendAuth{Mode: config.AuthModeStaticKeys, AccessKey: ak, SecretKey: sk},
	}
	const publicBucket, privateBucket = "public-photos", "private-docs"
	reg := s3adapter.NewBackendRegistry(config.Storage{Backends: map[string]config.StorageBackend{"sw": backend}})
	prov := s3adapter.NewProvisionerRouter(reg)
	for _, b := range []string{publicBucket, privateBucket} {
		if err := prov.CreateBucket(ctx, "sw", b, ""); err != nil {
			t.Fatalf("create %s: %v", b, err)
		}
	}
	if err := prov.SetAnonymousReadPolicy(ctx, "sw", publicBucket); err != nil {
		t.Fatalf("set policy: %v", err)
	}
	const key = "t/c/object"
	body := []byte("public bytes")
	signed, anonymous := seaweedClients(t, ep, ak, sk)
	for _, b := range []string{publicBucket, privateBucket} {
		if _, err := signed.PutObject(ctx, &s3.PutObjectInput{
			Bucket: aws.String(b), Key: aws.String(key), Body: bytes.NewReader(body),
		}); err != nil {
			t.Fatalf("put into %s: %v", b, err)
		}
	}
	if _, err := anonymous.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(publicBucket), Key: aws.String(key)}); err != nil {
		t.Errorf("unsigned GET from the public bucket: %v", err)
	}
	if _, err := anonymous.GetObject(ctx, &s3.GetObjectInput{Bucket: aws.String(privateBucket), Key: aws.String(key)}); err == nil {
		t.Error("an unsigned GET read the private bucket")
	}
	if _, err := anonymous.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(publicBucket)}); err == nil {
		t.Error("an unsigned request listed the public bucket")
	}
	if _, err := anonymous.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(publicBucket), Key: aws.String("t/c/planted"), Body: bytes.NewReader(body),
	}); err == nil {
		t.Error("an unsigned request wrote into the public bucket")
	}
}

// seaweedClients returns an S3 client signing as the test identity and one
// sending every request unsigned.
func seaweedClients(t *testing.T, endpoint, accessKey, secretKey string) (signed, anonymous *s3.Client) {
	t.Helper()
	mk := func(creds aws.CredentialsProvider) *s3.Client {
		return s3.New(s3.Options{
			Region: "us-east-1", BaseEndpoint: aws.String(endpoint), UsePathStyle: true, Credentials: creds,
		})
	}
	return mk(credentials.NewStaticCredentialsProvider(accessKey, secretKey, "")), mk(aws.AnonymousCredentials{})
}
