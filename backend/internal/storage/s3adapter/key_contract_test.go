package s3adapter

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/multiparth"
	"github.com/oleg-tkachuk/paladin/backend/internal/api/data/v1/objecth"
	"github.com/oleg-tkachuk/paladin/backend/internal/storage/storagepath"
)

// legacyKey is a key from before the key contract: the old composer wrote it
// at the path of "a/b".
const legacyKey = "a//b"

// Every call that addresses an object refuses a key the composer refuses,
// and sends nothing: no request reaches the store at another object's path.
func TestEveryObjectCallRefusesANonCanonicalKey(t *testing.T) {
	f := newFakeS3(t)
	c := newTestClient(t, f.srv.URL)
	ok := objecth.Location{TenantID: testTenant, Bucket: "b", Collection: "ok", Key: "k"}
	bad := ok
	bad.Key = legacyKey

	calls := map[string]func() error{
		"PresignPut": func() error {
			_, _, _, err := c.PresignPut(testCtx, objecth.PresignPutArgs{TenantID: testTenant, Bucket: "b", Collection: "ok", Key: legacyKey})
			return err
		},
		"PresignPost": func() error {
			_, _, _, err := c.PresignPost(testCtx, objecth.PresignPostArgs{TenantID: testTenant, Bucket: "b", Collection: "ok", Key: legacyKey, ChecksumAlgo: "SHA256"})
			return err
		},
		"PresignGet": func() error {
			_, _, _, err := c.PresignGet(testCtx, objecth.PresignGetArgs{TenantID: testTenant, Bucket: "b", Collection: "ok", Key: legacyKey})
			return err
		},
		"Head": func() error {
			_, _, _, _, err := c.Head(testCtx, "b", testTenant, "ok", legacyKey, "")
			return err
		},
		"CopyObject destination": func() error { return c.CopyObject(testCtx, ok, bad) },
		"CopyObject source":      func() error { return c.CopyObject(testCtx, bad, ok) },
		"GetStream": func() error {
			_, _, err := c.GetStream(testCtx, "b", testTenant, "ok", legacyKey)
			return err
		},
		"DeleteObject": func() error { return c.DeleteObject(testCtx, "b", testTenant, "ok", legacyKey) },
		"InitiateMultipart": func() error {
			_, err := c.InitiateMultipart(testCtx, "b", testTenant, "ok", legacyKey, "", "", "")
			return err
		},
		"CompleteMultipart": func() error {
			_, _, err := c.CompleteMultipart(testCtx, "b", testTenant, fakeUploadID, "ok", legacyKey, "", nil)
			return err
		},
		"ListMultipartParts": func() error {
			_, _, err := c.ListMultipartParts(testCtx, "b", testTenant, fakeUploadID, "ok", legacyKey, 0, 0)
			return err
		},
		"AbortMultipart": func() error { return c.AbortMultipart(testCtx, "b", testTenant, fakeUploadID, "ok", legacyKey) },
		"PresignPart": func() error {
			_, _, _, err := c.PresignPart(testCtx, "b", testTenant, fakeUploadID, "ok", legacyKey, multiparth.PartBinding{Number: 1}, 0)
			return err
		},
		"Open": func() error {
			_, err := c.Open(testCtx, "b", testTenant, "ok", legacyKey, "", 0)
			return err
		},
		"PublicURL": func() error {
			_, err := c.PublicURL(testCtx, "b", "", testTenant, "ok", legacyKey)
			return err
		},
	}
	for name, call := range calls {
		if err := call(); !errors.Is(err, storagepath.ErrNotCanonical) {
			t.Errorf("%s: err = %v, want a key refusal", name, err)
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.reqs) != 0 {
		t.Errorf("%d requests reached the store for refused keys", len(f.reqs))
	}
}

// ListMultipartParts reads back what the store recorded: each part's number,
// etag, size and whichever checksum it carries, and the marker to page from.
func TestListMultipartParts(t *testing.T) {
	const (
		sha256Part, crc32cPart = 1, 2
		partSize               = 5242880
	)
	f := newFakeS3(t)
	f.route = func(w http.ResponseWriter, r *http.Request, _, _ string) bool {
		if r.Method != http.MethodGet || !r.URL.Query().Has("uploadId") {
			return false
		}
		writeXML(w, http.StatusOK, fmt.Sprintf(`<ListPartsResult><IsTruncated>true</IsTruncated>`+
			`<Part><PartNumber>%d</PartNumber><ETag>&quot;e1&quot;</ETag><Size>%d</Size><LastModified>2026-01-01T00:00:00Z</LastModified><ChecksumSHA256>s1</ChecksumSHA256></Part>`+
			`<Part><PartNumber>%d</PartNumber><ETag>&quot;e2&quot;</ETag><Size>%d</Size><ChecksumCRC32C>c2</ChecksumCRC32C></Part>`+
			`</ListPartsResult>`, sha256Part, partSize, crc32cPart, partSize))
		return true
	}
	c := newTestClient(t, f.srv.URL)
	parts, next, err := c.ListMultipartParts(testCtx, "b", testTenant, fakeUploadID, "ok", "k", 2, 0)
	if err != nil {
		t.Fatalf("ListMultipartParts: %v", err)
	}
	if len(parts) != 2 || next != crc32cPart {
		t.Fatalf("parts = %+v, next = %d", parts, next)
	}
	if p := parts[0]; p.PartNumber != sha256Part || p.ETag != "e1" || p.SizeBytes != partSize || p.Checksum != "s1" || p.UploadedAt.IsZero() {
		t.Errorf("first part = %+v", p)
	}
	if p := parts[1]; p.Checksum != "c2" {
		t.Errorf("second part = %+v", p)
	}
	reqs := f.requestsFor(http.MethodGet, mustComposeKey(t, testTenant, "ok", "k"))
	if len(reqs) != 1 || reqs[0].Query.Get("max-parts") != "2" {
		t.Errorf("requests = %+v, want one ListParts with max-parts", reqs)
	}
}
