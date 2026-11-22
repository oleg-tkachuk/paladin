//go:build integration

package components

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"io"
	"net/http"
	"strings"
	"testing"
	"uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/checksum"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/storage/s3adapter"
	"github.com/oleg-tkachuk/paladin/backend/internal/storage/storagepath"
)

// The grammar's longest collection: four segments of 63 characters, 255 in
// all, which the bucket path writes as one segment.
var longestCollection = strings.Repeat(strings.Repeat("c", 63)+"/", 3) + strings.Repeat("c", 63)

// keyTooLong is the error code SeaweedFS answers a path it cannot hold with.
const keyTooLong = "KeyTooLongError"

// The key limits are the store's: the longest key the boundary admits, in
// the longest collection, is a path of exactly the store's limit, which a
// filer-backed store takes and reads back; one segment a byte longer than the
// boundary admits is refused by the store itself, though the path is short.
func TestTheKeyLimitsAreSeaweedFSs(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	ep, ak, sk := startS3(t)
	const backendID, bucket = "sw", "long-keys"
	reg := s3adapter.NewBackendRegistry(config.Storage{Backends: map[string]config.StorageBackend{backendID: {
		Region: "us-east-1", Endpoint: ep, ForcePathStyle: true,
		Auth: config.StorageBackendAuth{Mode: config.AuthModeStaticKeys, AccessKey: ak, SecretKey: sk},
	}}})
	if err := s3adapter.NewProvisionerRouter(reg).CreateBucket(ctx, backendID, bucket, ""); err != nil {
		t.Fatal(err)
	}
	objects := s3adapter.NewObjectRouter(reg)
	tenant := uuid.New()
	body := []byte("bytes at a long path")
	sum := sha256.Sum256(body)

	put := func(collection, key string) (int, string) {
		t.Helper()
		putURL, headers, _, err := objects.PresignPut(ctx, objecth.PresignPutArgs{
			BackendID: backendID, TenantID: tenant, Bucket: bucket, Collection: collection, Key: key,
			ContentType: "application/octet-stream", SizeBytes: int64(len(body)),
			ChecksumAlgo: checksum.SHA256, ChecksumValue: base64.StdEncoding.EncodeToString(sum[:]),
			TTL: presignTTL,
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
		answer, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		return resp.StatusCode, string(answer)
	}

	segment := strings.Repeat("k", storagepath.MaxSegmentBytes)
	longest := segment + "/" + segment + "/" + strings.Repeat("k", storagepath.MaxKeyBytes-2*(storagepath.MaxSegmentBytes+1))
	if err := storagepath.ValidateKey(longest); err != nil || len(longest) != storagepath.MaxKeyBytes {
		t.Fatalf("the longest key is %d bytes, %v", len(longest), err)
	}
	if path, err := storagepath.Compose(tenant, longestCollection, longest); err != nil || len(path) != storagepath.MaxPathBytes {
		t.Fatalf("its path is %d bytes, %v; want exactly %d", len(path), err, storagepath.MaxPathBytes)
	}
	if status, answer := put(longestCollection, longest); status != http.StatusOK {
		t.Fatalf("PUT at a %d-byte path answered %d: %s", storagepath.MaxPathBytes, status, answer)
	}
	if _, size, _, _, err := objects.Head(ctx, backendID, bucket, tenant, longestCollection, longest, checksum.SHA256); err != nil || size != int64(len(body)) {
		t.Fatalf("Head = %d bytes, %v; want %d", size, err, len(body))
	}

	overSegment := segment + "k"
	if storagepath.ValidateKey(overSegment) == nil {
		t.Fatalf("a segment of %d bytes is admitted", len(overSegment))
	}
	if status, answer := put("docs", overSegment); status != http.StatusBadRequest || !strings.Contains(answer, keyTooLong) {
		t.Errorf("PUT with a %d-byte segment answered %d %s, want the store's %s", len(overSegment), status, answer, keyTooLong)
	}
}
