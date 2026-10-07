//go:build integration

package components

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/checksum"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/publicread"
	"github.com/oleg-tkachuk/paladin/backend/internal/storage/s3adapter"
)

// presignTTL bounds the test's own upload URL.
const presignTTL = time.Minute

// An object uploaded into a public bucket through the URL Paladin signs is
// served, unsigned, at the URL Paladin reports, with the collection's
// Cache-Control; once deleted, the store answers 404 there (ADR-0027).
func TestPublicObjectRoundTripOnSeaweedFS(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ep, ak, sk := startS3(t)
	const backendID, bucket, collection = "sw", "public-photos", "photos"
	reg := s3adapter.NewBackendRegistry(config.Storage{Backends: map[string]config.StorageBackend{backendID: {
		Region: "us-east-1", Endpoint: ep, PublicEndpoint: ep, ForcePathStyle: true,
		Auth: config.StorageBackendAuth{Mode: config.AuthModeStaticKeys, AccessKey: ak, SecretKey: sk},
	}}})
	prov := s3adapter.NewProvisionerRouter(reg)
	if err := prov.CreateBucket(ctx, backendID, bucket, ""); err != nil {
		t.Fatal(err)
	}
	if err := prov.SetAnonymousReadPolicy(ctx, backendID, bucket); err != nil {
		t.Fatal(err)
	}
	objects := s3adapter.NewObjectRouter(reg)
	tenant := uuid.New()
	key, err := publicread.NewKey()
	if err != nil {
		t.Fatal(err)
	}
	body := []byte("a published picture")
	sum := sha256.Sum256(body)

	putURL, headers, _, err := objects.PresignPut(ctx, objecth.PresignPutArgs{
		BackendID: backendID, TenantID: tenant, Bucket: bucket, Collection: collection, Key: key,
		ContentType: "image/jpeg", SizeBytes: int64(len(body)),
		ChecksumAlgo: checksum.SHA256, ChecksumValue: base64.StdEncoding.EncodeToString(sum[:]),
		TTL: presignTTL, CacheControl: publicread.DefaultCacheControl,
	})
	if err != nil {
		t.Fatal(err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, putURL, bytes.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.ContentLength = int64(len(body))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("signed PUT answered %d", resp.StatusCode)
	}

	publicURL, err := objects.PublicURL(ctx, backendID, bucket, "", tenant, collection, key)
	if err != nil {
		t.Fatal(err)
	}
	got, err := http.Get(publicURL) //nolint:noctx // a stranger's plain GET is the point
	if err != nil {
		t.Fatal(err)
	}
	read, _ := io.ReadAll(got.Body)
	_ = got.Body.Close()
	if got.StatusCode != http.StatusOK || !bytes.Equal(read, body) {
		t.Fatalf("unsigned GET %s = %d %q, want the uploaded bytes", publicURL, got.StatusCode, read)
	}
	if cc := got.Header.Get("Cache-Control"); cc != publicread.DefaultCacheControl {
		t.Errorf("Cache-Control = %q, want %q", cc, publicread.DefaultCacheControl)
	}

	if err := objects.DeleteObject(ctx, backendID, bucket, tenant, collection, key); err != nil {
		t.Fatal(err)
	}
	gone, err := http.Get(publicURL) //nolint:noctx // as above
	if err != nil {
		t.Fatal(err)
	}
	_ = gone.Body.Close()
	if gone.StatusCode != http.StatusNotFound {
		t.Errorf("after delete the URL answers %d, want 404", gone.StatusCode)
	}
}
