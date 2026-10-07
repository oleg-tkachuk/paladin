// Package checksum names the checksum algorithms an upload may declare and
// checks the values clients send under them.
//
// A value is the base64 of the digest, the form S3 itself writes in
// x-amz-checksum-* and Content-MD5 — so the string a client sends is the
// string signed into the URL and compared with what the object store reports,
// with no conversion between them.
package checksum

import (
	"crypto/md5" //nolint:gosec // S3's Content-MD5, an integrity check, not a security one
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"hash"
	"hash/crc32"
	"strconv"
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

// newHash is each algorithm's digest.
var newHash = map[string]func() hash.Hash{
	CRC32C: func() hash.Hash { return crc32.New(crc32.MakeTable(crc32.Castagnoli)) },
	SHA256: sha256.New,
	MD5:    md5.New,
}

// compositeSep separates a composite's digest from its part count.
const compositeSep = "-"

// ErrNoParts is Composite given no part checksums.
var ErrNoParts = errors.New("checksum: a composite needs at least one part")

// Composite is the checksum of an object assembled from parts, in the form S3
// calls COMPOSITE: the base64 of algo's digest of the parts' raw digests,
// concatenated in part order, then "-" and the part count. Each part value
// is the base64 digest the part was uploaded under, and is checked as
// Validate checks one.
func Composite(algo string, parts []string) (string, error) {
	if len(parts) == 0 {
		return "", ErrNoParts
	}
	algo = strings.ToUpper(algo)
	if err := Validate(algo, parts[0]); err != nil {
		return "", err
	}
	h := newHash[algo]()
	for i, part := range parts {
		if err := Validate(algo, part); err != nil {
			return "", fmt.Errorf("part %d: %w", i+1, err)
		}
		raw, _ := base64.StdEncoding.DecodeString(part) // Validate decoded it
		h.Write(raw)
	}
	return base64.StdEncoding.EncodeToString(h.Sum(nil)) + compositeSep + strconv.Itoa(len(parts)), nil
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
