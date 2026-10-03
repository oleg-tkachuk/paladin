// Package checksum names the checksum algorithms an upload may declare and
// checks the values clients send under them.
//
// A value is the base64 of the digest, the form S3 itself writes in
// x-amz-checksum-* and Content-MD5 — so the string a client sends is the
// string signed into the URL and compared with what the object store reports,
// with no conversion between them.
package checksum

import (
	"encoding/base64"
	"fmt"
	"strings"
)

// Algorithm names, as stored in objects.checksum_algorithm's mapping and as
// the API's ChecksumAlgorithm enum decodes to.
const (
	CRC32C = "CRC32C"
	SHA256 = "SHA256"
	MD5    = "MD5"
)

// digestBytes is each algorithm's digest length.
var digestBytes = map[string]int{
	CRC32C: 4,
	SHA256: 32,
	MD5:    16,
}

// Known reports whether algo is an algorithm Paladin accepts.
func Known(algo string) bool {
	_, ok := digestBytes[strings.ToUpper(algo)]
	return ok
}

// Validate checks value is a base64 digest of the right length for algo.
// The length check is what catches the common mistake — a hex digest, or a
// SHA-256 sent under CRC32C — before it is signed into a URL that every
// upload would then fail.
func Validate(algo, value string) error {
	want, ok := digestBytes[strings.ToUpper(algo)]
	if !ok {
		return fmt.Errorf("unknown checksum algorithm %q", algo)
	}
	raw, err := base64.StdEncoding.DecodeString(value)
	if err != nil {
		return fmt.Errorf("checksum %q is not base64: %w", value, err)
	}
	if len(raw) != want {
		return fmt.Errorf("%s checksum is %d bytes, want %d", strings.ToUpper(algo), len(raw), want)
	}
	return nil
}
