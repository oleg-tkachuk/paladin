// Package storagepath is the one place an object's key inside its bucket is
// composed, and the one place the inputs it accepts are defined. The S3
// adapter writes and reads bytes at this path, and the data plane reports it
// to callers allowed to see an object's physical placement
// (Object.placement), so the two cannot disagree on where an object lives.
//
// The layout is "<tenant_id>/<collection segment>/<key>". Compose is
// injective: two different (tenant, collection, key) triples never share a
// path, so two objects never share bytes. Two rules make it so.
//
//   - A key is canonical (ValidateKey): segments separated by "/", none of
//     them empty, "." or "..". The path holds the key verbatim, so a key that
//     a store or a reader might normalise is refused rather than rewritten.
//   - The collection is ONE path segment. A collection name may itself hold
//     "/" (invoices/2026/q1), so its segments are joined with
//     CollectionSegmentSeparator, a character the collection grammar
//     forbids. Collection "a/b" with key "c" and collection "a" with key "b/c"
//     land at "<t>/a.b/c" and "<t>/a/b/c".
//
// A key that enters is held to more than its segments: valid UTF-8, no
// control character, segments of at most MaxSegmentBytes, and at most
// MaxKeyBytes in all, so that the composed path fits the store's
// MaxPathBytes in every collection. The bound is the key's alone,
// not the composed path's, so the boundary, the SDKs and the console check it
// without knowing the collection, and a key admitted in one collection stays
// admitted wherever it is copied.
//
// The package is a leaf: every path that creates an object imports it to
// refuse a key at the boundary. The composer refuses every input that would
// alias another object's path or that the store cannot hold, so a caller that
// forgets the boundary check fails closed; it does not repeat the boundary's
// control-character and key-length rules, so an object stored before them
// stays readable.
package storagepath

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
	"uuid"
)

// Separator separates the segments of a key and of a bucket path.
const Separator = "/"

// CollectionSegmentSeparator joins a collection's segments inside the one
// path segment the collection occupies. The collection grammar admits only
// [a-z0-9-] in a segment, so it never appears in a name and the encoding is
// reversible.
const CollectionSegmentSeparator = "."

// The key segments a path reader would resolve instead of reading verbatim.
const (
	currentSegment = "."
	parentSegment  = ".."
)

// collectionGrammar and maxCollectionLength are the collection-name grammar,
// the collections_name_format CHECK of the schema baseline
// (001_initial_schema.sql) spelled in Go: one or more kebab-case segments of
// at most 63 characters, separated by "/", at most 255 characters in all. The
// integration suite holds the two to the same cases.
const maxCollectionLength = 255

var collectionGrammar = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(/[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)

// MaxPathBytes is the longest bucket path a store takes: S3 limits an object
// key, which this path is, to 1024 bytes of UTF-8.
const MaxPathBytes = 1024

// tenantSegmentBytes is the length of the path's first segment, a tenant id
// in a UUID's canonical text form.
const tenantSegmentBytes = 36

// MaxKeyBytes is the longest key, in bytes, that composes to at most
// MaxPathBytes in every collection: the store's limit less the tenant
// segment, the longest collection segment (CollectionSegment keeps a name's
// length) and the two separators between them.
const MaxKeyBytes = MaxPathBytes - tenantSegmentBytes - len(Separator) - maxCollectionLength - len(Separator)

// MaxSegmentBytes is the longest key segment, in bytes, a filer-backed store
// takes: SeaweedFS keeps each segment as a file name and refuses one longer
// (KeyTooLongError), though the whole path is within MaxPathBytes. The
// longest collection segment is exactly this long.
const MaxSegmentBytes = 255

// ErrNotCanonical is what every refusal of this package wraps.
var ErrNotCanonical = errors.New("not canonical")

// KeyRule is the fixed sentence a refused key is answered with.
var KeyRule = fmt.Sprintf(`an object key is at most %d bytes of UTF-8 without control characters, in segments of at most %d bytes separated by "/", none of them empty, "." or ".."`, MaxKeyBytes, MaxSegmentBytes)

// KeyPrefixRule is the fixed sentence a refused key prefix is answered with.
var KeyPrefixRule = fmt.Sprintf(`a key prefix is the start of an object key: under %d bytes of UTF-8 without control characters, no leading "/", and no empty, "." or ".." segment, nor one over %d bytes, before its last "/"`, MaxKeyBytes, MaxSegmentBytes)

// PathRule is the fixed sentence a path the store cannot hold is refused with.
var PathRule = fmt.Sprintf("a storage path is at most %d bytes", MaxPathBytes)

// CollectionRule is the fixed sentence a refused collection is answered with.
const CollectionRule = `a collection is kebab-case segments separated by "/"`

// Error is a refused input: the rule it breaks and why.
type Error struct {
	Rule   string
	Reason string
}

func (e *Error) Error() string { return e.Rule + ": " + e.Reason }

// Is makes every refusal match ErrNotCanonical.
func (e *Error) Is(target error) bool { return target == ErrNotCanonical }

// The reasons a key or a prefix is refused.
const (
	reasonEmpty          = "it is empty"
	reasonLeading        = `it starts with "/"`
	reasonTrailing       = `it ends with "/"`
	reasonEmptySegment   = "it holds an empty segment"
	reasonCurrentSegment = `it holds a "." segment`
	reasonParentSegment  = `it holds a ".." segment`
	reasonGrammar        = "it does not match the collection grammar"
	reasonInvalidUTF8    = "it is not valid UTF-8"
	reasonControl        = "it holds a control character"
	// A path segment never holds the separator; a string that does is not one.
	reasonSegmentSeparator = `a path segment holds no "/"`
)

// ValidateKey refuses a key that is not canonical, is not valid UTF-8, holds
// a control character (Unicode category Cc) or is longer than MaxKeyBytes. A
// trailing "/" is refused too: an S3 directory marker composes to a path of
// its own, but a filer-backed store (SeaweedFS) keeps such a key as a
// directory and drops the bytes written to it, so an object there would read
// back empty.
func ValidateKey(key string) error {
	if reason := textReason(key, MaxKeyBytes); reason != "" {
		return &Error{Rule: KeyRule, Reason: reason}
	}
	if reason := shapeReason(key); reason != "" {
		return &Error{Rule: KeyRule, Reason: reason}
	}
	if reason := segmentLengthReason(strings.Split(key, Separator), MaxSegmentBytes); reason != "" {
		return &Error{Rule: KeyRule, Reason: reason}
	}
	return nil
}

// ValidateKeyPrefix refuses a prefix that no canonical key can start with. A
// prefix is joined to a key verbatim, so only its complete segments — those
// before its last "/" — are held to the key rule; what follows the last "/"
// is the start of a segment the key completes. A prefix leaves room for at
// least one byte of key. The empty prefix is no prefix.
func ValidateKeyPrefix(prefix string) error {
	if prefix == "" {
		return nil
	}
	if reason := textReason(prefix, MaxKeyBytes-1); reason != "" {
		return &Error{Rule: KeyPrefixRule, Reason: reason}
	}
	if strings.HasPrefix(prefix, Separator) {
		return &Error{Rule: KeyPrefixRule, Reason: reasonLeading}
	}
	segments := strings.Split(prefix, Separator)
	complete, partial := segments[:len(segments)-1], segments[len(segments)-1:]
	reason := segmentsReason(complete)
	if reason == "" {
		reason = segmentLengthReason(complete, MaxSegmentBytes)
	}
	if reason == "" {
		// The key completes the last segment by at least one byte.
		reason = segmentLengthReason(partial, MaxSegmentBytes-1)
	}
	if reason != "" {
		return &Error{Rule: KeyPrefixRule, Reason: reason}
	}
	return nil
}

// segmentLengthReason is why one of segments is longer than maxBytes, or "".
func segmentLengthReason(segments []string, maxBytes int) string {
	for _, s := range segments {
		if len(s) > maxBytes {
			return fmt.Sprintf("it holds a segment of %d bytes, over the limit of %d", len(s), maxBytes)
		}
	}
	return ""
}

// ValidateStoredKey refuses a key no object can have a path of its own
// under: not valid UTF-8, or with segments that would alias another key's
// path. It is the part of ValidateKey that Compose holds every key to, so an
// object stored before the boundary's length and control-character rules —
// and an event naming one — is still read, while a key ValidateKey refuses
// is never created.
func ValidateStoredKey(key string) error {
	reason := shapeReason(key)
	if !utf8.ValidString(key) {
		reason = reasonInvalidUTF8
	}
	if reason != "" {
		return &Error{Rule: KeyRule, Reason: reason}
	}
	return nil
}

// textReason is why s is not key text of at most maxBytes, or "".
func textReason(s string, maxBytes int) string {
	switch {
	case !utf8.ValidString(s):
		return reasonInvalidUTF8
	case len(s) > maxBytes:
		return tooLongReason(len(s), maxBytes)
	case strings.ContainsFunc(s, unicode.IsControl):
		return reasonControl
	}
	return ""
}

// tooLongReason names a length and the limit it is over.
func tooLongReason(length, limit int) string {
	return fmt.Sprintf("it is %d bytes, over the limit of %d", length, limit)
}

// shapeReason is why key's segments would let it alias another key's path,
// or "": the part of the key rule Compose holds every key to.
func shapeReason(key string) string {
	switch {
	case key == "":
		return reasonEmpty
	case strings.HasPrefix(key, Separator):
		return reasonLeading
	case strings.HasSuffix(key, Separator):
		return reasonTrailing
	}
	return segmentsReason(strings.Split(key, Separator))
}

// segmentsReason is why one of segments may not be a key segment, or "".
func segmentsReason(segments []string) string {
	for _, s := range segments {
		switch s {
		case "":
			return reasonEmptySegment
		case currentSegment:
			return reasonCurrentSegment
		case parentSegment:
			return reasonParentSegment
		}
	}
	return ""
}

// ValidateCollection refuses a collection name outside the collection grammar.
func ValidateCollection(collection string) error {
	if len(collection) > maxCollectionLength || !collectionGrammar.MatchString(collection) {
		return &Error{Rule: CollectionRule, Reason: reasonGrammar}
	}
	return nil
}

// CollectionSegment is the one path segment collection occupies.
func CollectionSegment(collection string) string {
	return strings.ReplaceAll(collection, Separator, CollectionSegmentSeparator)
}

// ParseCollectionSegment is the collection whose path segment is segment: the
// inverse of CollectionSegment. A segment no collection occupies is refused.
func ParseCollectionSegment(segment string) (string, error) {
	if strings.Contains(segment, Separator) {
		return "", &Error{Rule: CollectionRule, Reason: reasonSegmentSeparator}
	}
	collection := strings.ReplaceAll(segment, CollectionSegmentSeparator, Separator)
	if err := ValidateCollection(collection); err != nil {
		return "", err
	}
	return collection, nil
}

// Compose builds an object's path inside its bucket:
// "<tenant_id>/<collection segment>/<key>". It refuses a collection outside
// the grammar, a key that is not valid UTF-8 or whose segments are not
// canonical, and a path longer than MaxPathBytes, rather than rewriting any
// of them: a rewrite is exactly what let two keys share one path. There is no
// leading slash; S3 keys never start with one. Every key ValidateKey admits
// composes in every collection.
func Compose(tenantID uuid.UUID, collection, key string) (string, error) {
	if err := ValidateCollection(collection); err != nil {
		return "", fmt.Errorf("compose storage path: collection: %w", err)
	}
	if err := ValidateStoredKey(key); err != nil {
		return "", fmt.Errorf("compose storage path: key: %w", err)
	}
	path := tenantID.String() + Separator + CollectionSegment(collection) + Separator + key
	if len(path) > MaxPathBytes {
		return "", fmt.Errorf("compose storage path: %w", &Error{Rule: PathRule, Reason: tooLongReason(len(path), MaxPathBytes)})
	}
	return path, nil
}
