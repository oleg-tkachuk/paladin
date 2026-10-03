package checksum

import (
	"crypto/md5"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"hash/crc32"
	"testing"
)

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func TestValidate(t *testing.T) {
	body := []byte("hello")
	sha := sha256.Sum256(body)
	md := md5.Sum(body)
	crc := crc32.Checksum(body, crc32.MakeTable(crc32.Castagnoli))
	crcBytes := []byte{byte(crc >> 24), byte(crc >> 16), byte(crc >> 8), byte(crc)}

	cases := []struct {
		name, algo, value string
		ok                bool
	}{
		{"sha256", SHA256, b64(sha[:]), true},
		{"lower-case algorithm name", "sha256", b64(sha[:]), true},
		{"crc32c", CRC32C, b64(crcBytes), true},
		{"md5", MD5, b64(md[:]), true},
		{"hex instead of base64", SHA256, hex.EncodeToString(sha[:]), false},
		{"a sha256 under crc32c", CRC32C, b64(sha[:]), false},
		{"empty", SHA256, "", false},
		{"unknown algorithm", "SHA1", b64(sha[:20]), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := Validate(tc.algo, tc.value)
			if tc.ok != (err == nil) {
				t.Fatalf("Validate(%s, %q) = %v, want ok=%v", tc.algo, tc.value, err, tc.ok)
			}
		})
	}
}

func TestKnown(t *testing.T) {
	for _, a := range []string{CRC32C, SHA256, MD5, "md5"} {
		if !Known(a) {
			t.Errorf("%s not known", a)
		}
	}
	if Known("CRC64NVME") || Known("") {
		t.Error("an unsupported algorithm reported known")
	}
}
