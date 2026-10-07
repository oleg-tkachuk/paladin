package s3adapter

import (
	"context"
	"net/http"
	"slices"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/checksum"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/publicread"
)

const publicKey = "abcdefghijklmnopqrstuvwxyz"

func TestPublicURL(t *testing.T) {
	objectPath := testTenant.String() + "/photos/" + publicKey
	for name, tc := range map[string]struct {
		backend config.StorageBackend
		base    string
		want    string
	}{
		"path-style public endpoint": {
			config.StorageBackend{Region: "us-east-1", Endpoint: "http://seaweedfs:8333", PublicEndpoint: "https://s3.example", ForcePathStyle: true},
			"", "https://s3.example/pub/" + objectPath,
		},
		"host-style public endpoint": {
			config.StorageBackend{Region: "us-east-1", Endpoint: "http://seaweedfs:8333", PublicEndpoint: "https://s3.example"},
			"", "https://pub.s3.example/" + objectPath,
		},
		"no public endpoint falls back to the internal one": {
			config.StorageBackend{Region: "us-east-1", Endpoint: "http://seaweedfs:8333", ForcePathStyle: true},
			"", "http://seaweedfs:8333/pub/" + objectPath,
		},
		"AWS with no endpoint": {
			config.StorageBackend{Region: "eu-central-1"},
			"", "https://pub.s3.eu-central-1.amazonaws.com/" + objectPath,
		},
		"a CDN serves the bucket at its root": {
			config.StorageBackend{Region: "us-east-1", PublicEndpoint: "https://s3.example", ForcePathStyle: true},
			"https://cdn.example/assets", "https://cdn.example/assets/" + objectPath,
		},
	} {
		t.Run(name, func(t *testing.T) {
			c := newClient(aws.Config{Region: tc.backend.Region, Credentials: fakeCreds{}}, tc.backend)
			got, err := c.PublicURL(context.Background(), "pub", tc.base, testTenant, "photos", publicKey)
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("PublicURL = %q, want %q", got, tc.want)
			}
		})
	}
}

// A public collection's Cache-Control is signed into the PUT, so the client
// must send exactly it and the store keeps exactly it.
func TestPresignPutSignsCacheControl(t *testing.T) {
	c := clientWithCreds(t, fakeCreds{})
	u, hdrs, _, err := c.PresignPut(context.Background(), objecth.PresignPutArgs{
		TenantID: testTenant, Bucket: "b", Collection: "photos", Key: publicKey, ContentType: "image/jpeg",
		SizeBytes: 1, ChecksumAlgo: checksum.SHA256, ChecksumValue: sha256Empty, TTL: time.Minute,
		CacheControl: publicread.DefaultCacheControl,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(signedHeaderSet(t, u), "cache-control") || hdrs[cacheControlField] != publicread.DefaultCacheControl {
		t.Errorf("cache-control not bound: signed %v, headers %v", signedHeaderSet(t, u), hdrs)
	}
}

func TestPresignPutWithoutCacheControlSignsNone(t *testing.T) {
	c := clientWithCreds(t, fakeCreds{})
	u, hdrs, _, err := c.PresignPut(context.Background(), objecth.PresignPutArgs{
		TenantID: testTenant, Bucket: "b", Collection: "docs", Key: "k", ContentType: "image/jpeg",
		SizeBytes: 1, ChecksumAlgo: checksum.SHA256, ChecksumValue: sha256Empty, TTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(signedHeaderSet(t, u), "cache-control") || hdrs[cacheControlField] != "" {
		t.Error("a private upload was bound to a Cache-Control")
	}
}

func TestPresignPostBindsCacheControl(t *testing.T) {
	c := clientWithCreds(t, fakeCreds{})
	_, fields, _, err := c.PresignPost(context.Background(), objecth.PresignPostArgs{
		TenantID: testTenant, Bucket: "b", Collection: "photos", Key: publicKey, ContentType: "image/jpeg",
		SizeBytes: 1, ChecksumAlgo: checksum.SHA256, ChecksumValue: sha256Empty, TTL: time.Minute,
		CacheControl: publicread.DefaultCacheControl,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !hasCondition(postPolicy(t, fields), cacheControlField, publicread.DefaultCacheControl) ||
		fields[cacheControlField] != publicread.DefaultCacheControl {
		t.Errorf("cache-control not bound into the form: %v", fields)
	}
}

func TestInitiateMultipartSendsCacheControl(t *testing.T) {
	f := newFakeS3(t)
	c := newTestClient(t, f.srv.URL)
	if _, err := c.InitiateMultipart(testCtx, "b", testTenant, "photos", publicKey, "image/jpeg", publicread.DefaultCacheControl, checksum.SHA256); err != nil {
		t.Fatal(err)
	}
	creates := f.requestsFor(http.MethodPost, "")
	if len(creates) == 0 || creates[len(creates)-1].Header.Get(cacheControlField) != publicread.DefaultCacheControl {
		t.Errorf("initiate did not carry the Cache-Control")
	}
}

// A copy into a public collection replaces the copy's metadata with the
// collection's Cache-Control, restating the content type the replace would
// otherwise drop; any other copy keeps the source's metadata.
func TestCopyObjectCacheControl(t *testing.T) {
	for name, tc := range map[string]struct {
		cacheControl string
		directive    string
	}{
		"into a public collection": {publicread.DefaultCacheControl, "REPLACE"},
		"anywhere else":            {"", ""},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFakeS3(t)
			c := newTestClient(t, f.srv.URL)
			if err := c.CopyObject(testCtx,
				objecth.Location{TenantID: testTenant, Bucket: "b", Collection: "src", Key: "k"},
				objecth.Location{TenantID: testTenant, Bucket: "b", Collection: "photos", Key: publicKey,
					CacheControl: tc.cacheControl, ContentType: "image/jpeg"}); err != nil {
				t.Fatal(err)
			}
			puts := f.requestsFor(http.MethodPut, "")
			h := puts[len(puts)-1].Header
			if h.Get("X-Amz-Metadata-Directive") != tc.directive || h.Get(cacheControlField) != tc.cacheControl {
				t.Errorf("copy headers %v, want directive %q and cache-control %q", h, tc.directive, tc.cacheControl)
			}
			if tc.directive != "" && h.Get(contentTypeField) != "image/jpeg" {
				t.Errorf("a replacing copy dropped the content type: %v", h)
			}
		})
	}
}
