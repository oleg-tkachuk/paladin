// Package publicread holds the rules of public collections (ADR-0027):
// buckets whose objects anyone may read by URL, unsigned. Every rule here
// exists because a public object is authorised by its URL alone and served
// from the store's host to whoever asks.
package publicread

import (
	"crypto/rand"
	"encoding/base32"
	"errors"
	"fmt"
	"mime"
	"strings"

	"connectrpc.com/connect"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
	commonv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/common/v1"
)

// DefaultCacheControl is what a public collection stores its objects with
// when it names nothing else: a year, and immutable, because content changes
// by writing a new object under a new key.
const DefaultCacheControl = "public, max-age=31536000, immutable"

// MaxCacheControlLength bounds a collection's cache_control. The value is
// sent as a header on every upload and every read.
const MaxCacheControlLength = 256

// KeyBytes is the randomness in a public object's key: 128 bits, so that
// guessing one is not a way in.
const KeyBytes = 16

// keyEncoding spells a key in lowercase base32 without padding — 26
// characters, safe in a URL path and in an S3 key.
var keyEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// ErrRule is a request that breaks a rule of public collections.
var ErrRule = errors.New("public collection")

func init() {
	apiutil.RegisterError(ErrRule, connect.CodeFailedPrecondition,
		commonv1.ErrorReason_ERROR_REASON_PUBLIC_COLLECTION_RULE)
}

// Rulef wraps ErrRule with what was broken.
func Rulef(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrRule, fmt.Sprintf(format, args...))
}

// NewKey names a new object in a public collection.
func NewKey() (string, error) {
	b := make([]byte, KeyBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("public object key: %w", err)
	}
	return strings.ToLower(keyEncoding.EncodeToString(b)), nil
}

// activeTypes are media types a browser runs or renders as a document. Served
// to anyone from the store's host, each is stored cross-site scripting.
var activeTypes = map[string]bool{
	"text/html":                true,
	"application/xhtml+xml":    true,
	"image/svg+xml":            true,
	"text/xml":                 true,
	"application/xml":          true,
	"text/javascript":          true,
	"application/javascript":   true,
	"application/x-javascript": true,
	"text/ecmascript":          true,
	"application/ecmascript":   true,
	"text/xsl":                 true,
}

// xmlSuffix marks a structured-syntax XML type (RFC 6839), any of which a
// browser may render as a document.
const xmlSuffix = "+xml"

// IsActive reports whether a browser would run or render the content type as
// a document. A type that does not parse is treated as active.
func IsActive(contentType string) bool {
	mt, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return true
	}
	return activeTypes[mt] || strings.HasSuffix(mt, xmlSuffix)
}

// CheckAllowedTypes admits the allowed content types of a public bucket: at
// least one, and none active. An empty list allows every type.
func CheckAllowedTypes(types []string) error {
	if len(types) == 0 {
		return Rulef("a public bucket must list its allowed content types")
	}
	for _, t := range types {
		if IsActive(t) {
			return Rulef("content type %q is executed or rendered by a browser and may not be served publicly", t)
		}
	}
	return nil
}

// CacheControl returns the cache_control a public collection is created
// with: the requested value, or DefaultCacheControl when none was given.
func CacheControl(requested string) (string, error) {
	if requested == "" {
		return DefaultCacheControl, nil
	}
	if len(requested) > MaxCacheControlLength {
		return "", Rulef("cache_control is longer than %d characters", MaxCacheControlLength)
	}
	for _, r := range requested {
		if r < ' ' || r > '~' {
			return "", Rulef("cache_control holds a character a header may not")
		}
	}
	return requested, nil
}
