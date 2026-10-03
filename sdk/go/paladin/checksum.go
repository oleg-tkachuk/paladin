package paladin

import (
	"encoding/base64"
	"fmt"
	"io"
)

// Checksum is r's checksum under algo (ChecksumSHA256, ChecksumCRC32C or
// ChecksumMD5) in the form the API takes: base64 of the digest, as S3 writes
// it. An upload's URL is signed for this value, so it is what UploadObject's
// checksum_value and PresignPart's checksum_value carry — compute it over
// exactly the bytes the URL will be sent.
func Checksum(algo string, r io.Reader) (string, error) {
	newHash, ok := checksums[algo]
	if !ok {
		return "", fmt.Errorf("paladin: unknown checksum algorithm %q", algo)
	}
	h := newHash()
	if _, err := io.Copy(h, r); err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(h.Sum(nil)), nil
}
