package checksum

import (
	"crypto/sha256"
	"encoding/base64"
	"strconv"
	"strings"
	"testing"
)

// Any declared checksum: refused, or the one base64 spelling of a digest of
// the algorithm's length. The value is signed into the upload URL and later
// compared, as a string, with what the object store reports, so a second
// spelling of the same digest would pass here and fail every upload after.
func FuzzValidate(f *testing.F) {
	sha := sha256.Sum256([]byte("hello"))
	for _, c := range [][2]string{
		{SHA256, b64(sha[:])},
		{"sha256", b64(sha[:])},
		{CRC32C, "AAAAAA=="},
		{MD5, b64(sha[:16])},
		{SHA256, ""},
		{"SHA1", b64(sha[:20])},
	} {
		f.Add(c[0], c[1])
	}
	f.Fuzz(func(t *testing.T, algo, value string) {
		if err := Validate(algo, value); err != nil {
			return
		}
		raw, err := base64.StdEncoding.DecodeString(value)
		if err != nil {
			t.Fatalf("Validate(%s, %q) accepted what does not decode: %v", algo, value, err)
		}
		if got := b64(raw); got != value {
			t.Fatalf("Validate(%s, %q) accepted a second spelling of %q", algo, value, got)
		}
		if len(raw) != digestBytes[strings.ToUpper(algo)] {
			t.Fatalf("Validate(%s, %q) accepted a %d-byte digest", algo, value, len(raw))
		}
	})
}

// Any pair of part checksums: refused, or the composite of their digests,
// itself canonical base64 with the part count after it.
func FuzzComposite(f *testing.F) {
	sha := sha256.Sum256([]byte("hello"))
	f.Add(SHA256, b64(sha[:]), b64(sha[:]))
	f.Add(CRC32C, "AAAAAA==", "AAAAAB==")
	f.Fuzz(func(t *testing.T, algo, first, second string) {
		got, err := Composite(algo, []string{first, second})
		if err != nil {
			return
		}
		const parts = 2
		digest, ok := strings.CutSuffix(got, compositeSep+strconv.Itoa(parts))
		if !ok {
			t.Fatalf("Composite(%s) = %q, want a %d-part suffix", algo, got, parts)
		}
		if err := Validate(algo, digest); err != nil {
			t.Fatalf("Composite(%s) = %q, its digest refused: %v", algo, got, err)
		}
	})
}
