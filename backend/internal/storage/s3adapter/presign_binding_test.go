package s3adapter

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/multiparth"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/checksum"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
)

// Valid digests per algorithm (of the empty body; only the shape matters to
// the signer).
const (
	sha256Empty = "47DEQpj8HBSa+/TImW+5JCeuQeRkm5NMpJWZG3hSuFU="
	crc32cEmpty = "AAAAAA=="
	md5Empty    = "1B2M2Y8AsgTpgAmY7PhCfg=="
)

func signedHeaderSet(t *testing.T, raw string) []string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(u.Query().Get("X-Amz-SignedHeaders"), ";")
}

// A presigned PUT signed only Content-Type: size_hint and the checksum were
// dropped, so the URL accepted a body of any size and content and could
// overwrite a committed object. Each is now a signed header the store
// enforces.
func TestPresignPutBindsTheBody(t *testing.T) {
	cases := []struct {
		algo, value, header string
	}{
		{checksum.SHA256, sha256Empty, "x-amz-checksum-sha256"},
		{checksum.CRC32C, crc32cEmpty, "x-amz-checksum-crc32c"},
		{checksum.MD5, md5Empty, "content-md5"},
	}
	for _, tc := range cases {
		t.Run(tc.algo, func(t *testing.T) {
			c := clientWithCreds(t, fakeCreds{})
			u, hdrs, _, err := c.PresignPut(context.Background(), objecth.PresignPutArgs{
				TenantID: testTenant, Bucket: "b", Collection: "ok", Key: "k", ContentType: "image/png",
				SizeBytes: 1234, ChecksumAlgo: tc.algo, ChecksumValue: tc.value, TTL: time.Minute,
			})
			if err != nil {
				t.Fatal(err)
			}
			signed := signedHeaderSet(t, u)
			for _, want := range []string{"content-length", "content-type", "if-none-match", tc.header} {
				if !slices.Contains(signed, want) {
					t.Errorf("%s not signed: %v", want, signed)
				}
			}
			if hdrs["Content-Length"] != "1234" || hdrs["If-None-Match"] != "*" || hdrs["Content-Type"] != "image/png" {
				t.Errorf("required headers = %v", hdrs)
			}
			// The SDK must not add a checksum of its own: given only an
			// algorithm it signs the digest of an empty body.
			if strings.Contains(u, "X-Amz-Sdk-Checksum-Algorithm") {
				t.Errorf("SDK checksum algorithm leaked into the URL: %s", u)
			}
		})
	}
}

func TestPresignPutRefusesAMalformedChecksum(t *testing.T) {
	c := clientWithCreds(t, fakeCreds{})
	_, _, _, err := c.PresignPut(context.Background(), objecth.PresignPutArgs{
		TenantID: testTenant, Bucket: "b", Collection: "ok", Key: "k", ContentType: "image/png",
		SizeBytes: 1, ChecksumAlgo: checksum.SHA256, ChecksumValue: crc32cEmpty, TTL: time.Minute,
	})
	if err == nil {
		t.Fatal("signed a CRC32C value as a SHA-256")
	}
}

func TestPresignPutSignsServerSideEncryption(t *testing.T) {
	c := newClient(aws.Config{Region: "us-east-1", Credentials: fakeCreds{}}, config.StorageBackend{
		Bucket: "b", Region: "us-east-1", Endpoint: "http://s3.test:8333", ForcePathStyle: true,
		SSE: config.StorageBackendSSE{Type: "aws:kms", KeyID: "key-1"},
	})
	u, _, _, err := c.PresignPut(context.Background(), objecth.PresignPutArgs{
		TenantID: testTenant, Bucket: "b", Collection: "ok", Key: "k", ContentType: "image/png",
		SizeBytes: 1, ChecksumAlgo: checksum.SHA256, ChecksumValue: sha256Empty, TTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(signedHeaderSet(t, u), "x-amz-server-side-encryption") {
		t.Fatal("SSE header not signed")
	}
}

// postPolicy decodes the policy document a presigned POST carries.
func postPolicy(t *testing.T, fields map[string]string) []any {
	t.Helper()
	raw, err := base64.StdEncoding.DecodeString(fields["policy"])
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Conditions []any `json:"conditions"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc.Conditions
}

func hasCondition(conds []any, key, value string) bool {
	for _, c := range conds {
		if m, ok := c.(map[string]any); ok && m[key] == value {
			return true
		}
	}
	return false
}

func hasLengthRange(conds []any, n float64) bool {
	for _, c := range conds {
		if a, ok := c.([]any); ok && len(a) == 3 && a[0] == contentLengthRange && a[1] == n && a[2] == n {
			return true
		}
	}
	return false
}

// The "POST" transport returned a PUT URL with made-up fields that nothing
// enforced. It is now a real POST policy whose conditions bind the exact
// size, the Content-Type and the checksum.
func TestPresignPostIsAPolicyBindingTheBody(t *testing.T) {
	for _, tc := range []struct{ algo, value, field string }{
		{checksum.SHA256, sha256Empty, checksumSHA256Form},
		{checksum.CRC32C, crc32cEmpty, checksumCRC32CForm},
		{checksum.MD5, md5Empty, contentMD5Field},
	} {
		t.Run(tc.algo, func(t *testing.T) {
			c := clientWithCreds(t, fakeCreds{})
			action, fields, exp, err := c.PresignPost(context.Background(), objecth.PresignPostArgs{
				TenantID: testTenant, Bucket: "b", Collection: "ok", Key: "k", ContentType: "image/png",
				SizeBytes: 4096, ChecksumAlgo: tc.algo, ChecksumValue: tc.value, TTL: time.Minute,
			})
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasSuffix(action, "/b") {
				t.Errorf("action %q is not the bucket endpoint", action)
			}
			conds := postPolicy(t, fields)
			if !hasLengthRange(conds, 4096) {
				t.Errorf("no content-length-range [4096, 4096] in %v", conds)
			}
			if !hasCondition(conds, contentTypeField, "image/png") || !hasCondition(conds, tc.field, tc.value) {
				t.Errorf("Content-Type or checksum not a condition: %v", conds)
			}
			if !hasCondition(conds, "key", composeKey(testTenant, "ok", "k")) {
				t.Errorf("key not bound: %v", conds)
			}
			if fields[contentTypeField] != "image/png" || fields[tc.field] != tc.value {
				t.Errorf("form fields lack the values the policy requires: %v", fields)
			}
			if d := time.Until(exp); d > time.Minute || d < time.Minute-5*time.Second {
				t.Errorf("expiry %v is not the policy's", exp)
			}
		})
	}
}

func TestPresignPartBindsLengthAndChecksum(t *testing.T) {
	c := clientWithCreds(t, fakeCreds{})
	u, hdrs, _, err := c.PresignPart(context.Background(), "b", testTenant, "up-1", "ok", "k",
		multiparth.PartBinding{Number: 2, SizeBytes: 5 << 20, ChecksumAlgo: checksum.CRC32C, ChecksumValue: crc32cEmpty}, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	signed := signedHeaderSet(t, u)
	if !slices.Contains(signed, "content-length") || !slices.Contains(signed, "x-amz-checksum-crc32c") {
		t.Fatalf("part URL signs %v, want its length and checksum", signed)
	}
	if hdrs["Content-Length"] != "5242880" || hdrs["X-Amz-Checksum-Crc32c"] != crc32cEmpty {
		t.Fatalf("required headers = %v", hdrs)
	}
}

func TestPresignGetIfMatch(t *testing.T) {
	c := clientWithCreds(t, fakeCreds{})
	u, hdrs, _, err := c.PresignGet(context.Background(), objecth.PresignGetArgs{
		TenantID: testTenant, Bucket: "b", Collection: "ok", Key: "k", TTL: time.Minute, IfMatch: "abc123",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(signedHeaderSet(t, u), "if-match") || hdrs["If-Match"] != `"abc123"` {
		t.Fatalf("If-Match not bound: signed=%v headers=%v", signedHeaderSet(t, u), hdrs)
	}

	u, hdrs, _, err = c.PresignGet(context.Background(), objecth.PresignGetArgs{
		TenantID: testTenant, Bucket: "b", Collection: "ok", Key: "k", TTL: time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if slices.Contains(signedHeaderSet(t, u), "if-match") || hdrs["If-Match"] != "" {
		t.Fatal("If-Match signed although none was asked for")
	}
}

// Multipart uploads are opened with the checksum algorithm, and the
// completion list carries each part's checksum for the store to check.
func TestMultipartCarriesChecksums(t *testing.T) {
	f := newFakeS3(t)
	c := newTestClient(t, f.srv.URL)
	if _, err := c.InitiateMultipart(testCtx, "b", testTenant, "ok", "k", "image/png", "", checksum.SHA256); err != nil {
		t.Fatal(err)
	}
	creates := f.requestsFor(http.MethodPost, "")
	if len(creates) == 0 || creates[len(creates)-1].Header.Get("X-Amz-Checksum-Algorithm") != "SHA256" {
		t.Fatalf("CreateMultipartUpload did not name the algorithm: %+v", creates)
	}
	if _, _, err := c.CompleteMultipart(testCtx, "b", testTenant, fakeUploadID, "ok", "k", checksum.SHA256,
		[]multiparth.PartETag{{PartNumber: 1, ETag: "e1", ChecksumValue: sha256Empty}}); err != nil {
		t.Fatal(err)
	}
	var body string
	for _, r := range f.requestsFor(http.MethodPost, "") {
		if r.Query.Has("uploadId") {
			body = string(r.Body)
		}
	}
	if !strings.Contains(body, "<ChecksumSHA256>"+sha256Empty+"</ChecksumSHA256>") {
		t.Fatalf("completion body lacks the part checksum: %s", body)
	}
}
