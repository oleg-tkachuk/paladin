package s3adapter

import (
	"crypto/md5" // #nosec G501 — test parity check against the adapter's own digest
	"net/http"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	v4 "github.com/aws/aws-sdk-go-v2/aws/signer/v4"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"

	"github.com/oleg-tkachuk/paladin-private/internal/api/v1/object"
	"github.com/oleg-tkachuk/paladin-private/internal/config"
)

// TestSignedHeaders proves the v4 signed-header flattener: multi-value
// headers collapse to their first value, empty-value headers are dropped,
// and an empty input yields an empty (non-nil) map.
func TestSignedHeaders(t *testing.T) {
	t.Run("flattens first value and drops empties", func(t *testing.T) {
		req := &v4.PresignedHTTPRequest{
			SignedHeader: http.Header{
				"Host":      []string{"example.com"},
				"X-Amz-Foo": []string{"a", "b"}, // only first kept
				"X-Empty":   []string{},         // dropped
			},
		}
		got := signedHeaders(req)
		if got["Host"] != "example.com" {
			t.Errorf("Host = %q, want %q", got["Host"], "example.com")
		}
		if got["X-Amz-Foo"] != "a" {
			t.Errorf("X-Amz-Foo = %q, want first value %q", got["X-Amz-Foo"], "a")
		}
		if _, ok := got["X-Empty"]; ok {
			t.Errorf("empty-valued header must be omitted, got %q", got["X-Empty"])
		}
		if len(got) != 2 {
			t.Errorf("map size = %d, want 2 (Host + X-Amz-Foo)", len(got))
		}
	})

	t.Run("empty header set yields empty map", func(t *testing.T) {
		got := signedHeaders(&v4.PresignedHTTPRequest{SignedHeader: http.Header{}})
		if got == nil {
			t.Fatal("signedHeaders returned nil map, want empty non-nil map")
		}
		if len(got) != 0 {
			t.Errorf("map size = %d, want 0", len(got))
		}
	})
}

func TestApplySSE(t *testing.T) {
	cases := []struct {
		name       string
		sseType    string
		sseKey     string
		wantSSE    s3types.ServerSideEncryption
		wantKeyPtr bool
		wantKeyVal string
	}{
		{"empty leaves no SSE", "", "", "", false, ""},
		{"aes256 upper", "AES256", "", s3types.ServerSideEncryptionAes256, false, ""},
		{"aes256 lower is case-insensitive", "aes256", "ignored", s3types.ServerSideEncryptionAes256, false, ""},
		{"kms with key", "aws:kms", "key-123", s3types.ServerSideEncryptionAwsKms, true, "key-123"},
		{"kms without key", "aws:kms", "", s3types.ServerSideEncryptionAwsKms, false, ""},
		{"unknown type leaves no SSE", "garbage", "key-123", "", false, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			c := &Client{sseType: tc.sseType, sseKey: tc.sseKey}

			// PutObjectInput path (applySSE).
			put := &s3.PutObjectInput{}
			c.applySSE(put)
			if put.ServerSideEncryption != tc.wantSSE {
				t.Errorf("applySSE: ServerSideEncryption = %q, want %q", put.ServerSideEncryption, tc.wantSSE)
			}
			if tc.wantKeyPtr {
				if put.SSEKMSKeyId == nil || aws.ToString(put.SSEKMSKeyId) != tc.wantKeyVal {
					t.Errorf("applySSE: SSEKMSKeyId = %v, want %q", put.SSEKMSKeyId, tc.wantKeyVal)
				}
			} else if put.SSEKMSKeyId != nil {
				t.Errorf("applySSE: SSEKMSKeyId = %q, want nil", aws.ToString(put.SSEKMSKeyId))
			}

			// CreateMultipartUploadInput path (applyMultipartSSE) — must mirror.
			mp := &s3.CreateMultipartUploadInput{}
			c.applyMultipartSSE(mp)
			if mp.ServerSideEncryption != tc.wantSSE {
				t.Errorf("applyMultipartSSE: ServerSideEncryption = %q, want %q", mp.ServerSideEncryption, tc.wantSSE)
			}
			if tc.wantKeyPtr {
				if mp.SSEKMSKeyId == nil || aws.ToString(mp.SSEKMSKeyId) != tc.wantKeyVal {
					t.Errorf("applyMultipartSSE: SSEKMSKeyId = %v, want %q", mp.SSEKMSKeyId, tc.wantKeyVal)
				}
			} else if mp.SSEKMSKeyId != nil {
				t.Errorf("applyMultipartSSE: SSEKMSKeyId = %q, want nil", aws.ToString(mp.SSEKMSKeyId))
			}
		})
	}
}

func TestResolveBucket(t *testing.T) {
	c := &Client{cfg: config.StorageBackend{Bucket: "default-bkt"}}
	if got := c.resolveBucket("per-call"); got != "per-call" {
		t.Errorf("per-call bucket wins: got %q, want %q", got, "per-call")
	}
	if got := c.resolveBucket(""); got != "default-bkt" {
		t.Errorf("empty per-call falls back to config: got %q, want %q", got, "default-bkt")
	}

	empty := &Client{cfg: config.StorageBackend{}}
	if got := empty.resolveBucket(""); got != "" {
		t.Errorf("neither set: got %q, want empty (SDK rejects loudly)", got)
	}
}

func TestCompletionMode(t *testing.T) {
	impl := &Client{mode: object.CompletionModeImplicit}
	if got := impl.CompletionMode("any-key"); got != object.CompletionModeImplicit {
		t.Errorf("implicit client: got %v, want Implicit", got)
	}
	// objectKey is ignored — same mode regardless of argument.
	if got := impl.CompletionMode(""); got != object.CompletionModeImplicit {
		t.Errorf("objectKey must be ignored: got %v, want Implicit", got)
	}

	expl := &Client{mode: object.CompletionModeExplicit}
	if got := expl.CompletionMode("any-key"); got != object.CompletionModeExplicit {
		t.Errorf("explicit client: got %v, want Explicit", got)
	}
}

func TestPresignViewWrapsSameClient(t *testing.T) {
	c := &Client{}
	pv := c.Presign()
	if pv == nil {
		t.Fatal("Presign() returned nil")
	}
	if pv.c != c {
		t.Error("Presign() must wrap the same *Client instance")
	}
}

// TestStreamingMD5 proves the folded digest across chunked Write calls equals
// a one-shot md5 of the concatenated bytes — the checksum surfaced by
// StreamWriter.Close.
func TestStreamingMD5(t *testing.T) {
	full := []byte("the quick brown fox jumps over the lazy dog")
	want := md5.Sum(full) // #nosec G401 — parity check, not security

	h := newStreamingMD5()
	// Feed in three uneven chunks to exercise the folding.
	h.Write(full[:5])
	h.Write(full[5:20])
	h.Write(full[20:])
	got := h.Sum()

	if got != want {
		t.Errorf("streamingMD5.Sum() = %x, want %x", got, want)
	}

	// Empty stream: digest of no bytes.
	if got, want := newStreamingMD5().Sum(), md5.Sum(nil); got != want { // #nosec G401
		t.Errorf("empty streamingMD5.Sum() = %x, want %x", got, want)
	}
}

func TestParseSize(t *testing.T) {
	cases := map[string]int64{
		"0":     0,
		"123":   123,
		"-5":    -5,
		"":      0, // ParseInt error → 0
		"abc":   0,
		"12.5":  0,
		"  10 ": 0, // whitespace not trimmed → error → 0
	}
	for in, want := range cases {
		if got := parseSize(in); got != want {
			t.Errorf("parseSize(%q) = %d, want %d", in, got, want)
		}
	}
}
