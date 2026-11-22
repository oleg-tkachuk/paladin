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
// The package is a leaf: every path that creates an object imports it to
// refuse a key at the boundary, and the composer refuses the same inputs, so
// a caller that forgets the boundary check fails closed.
package storagepath

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
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

// ErrNotCanonical is what every refusal of this package wraps.
var ErrNotCanonical = errors.New("not canonical")

// KeyRule is the fixed sentence a refused key is answered with.
const KeyRule = `an object key is segments separated by "/", none of them empty, "." or ".."`

// KeyPrefixRule is the fixed sentence a refused key prefix is answered with.
const KeyPrefixRule = `a key prefix is the start of an object key: no leading "/", and no empty, "." or ".." segment before its last "/"`

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
	// A path segment never holds the separator; a string that does is not one.
	reasonSegmentSeparator = `a path segment holds no "/"`
)

// ValidateKey refuses a key that is not canonical. A trailing "/" is refused
// too: an S3 directory marker composes to a path of its own, but a
// filer-backed store (SeaweedFS) keeps such a key as a directory and drops
// the bytes written to it, so an object there would read back empty.
func ValidateKey(key string) error {
	if key == "" {
		return &Error{Rule: KeyRule, Reason: reasonEmpty}
	}
	if strings.HasPrefix(key, Separator) {
		return &Error{Rule: KeyRule, Reason: reasonLeading}
	}
	if strings.HasSuffix(key, Separator) {
		return &Error{Rule: KeyRule, Reason: reasonTrailing}
	}
	if reason := segmentsReason(strings.Split(key, Separator)); reason != "" {
		return &Error{Rule: KeyRule, Reason: reason}
	}
	return nil
}

// ValidateKeyPrefix refuses a prefix that no canonical key can start with. A
// prefix is joined to a key verbatim, so only its complete segments — those
// before its last "/" — are held to the key rule; what follows the last "/"
// is the start of a segment the key completes. The empty prefix is no prefix.
func ValidateKeyPrefix(prefix string) error {
	if prefix == "" {
		return nil
	}
	if strings.HasPrefix(prefix, Separator) {
		return &Error{Rule: KeyPrefixRule, Reason: reasonLeading}
	}
	segments := strings.Split(prefix, Separator)
	if reason := segmentsReason(segments[:len(segments)-1]); reason != "" {
		return &Error{Rule: KeyPrefixRule, Reason: reason}
	}
	return nil
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
// the grammar and a key that is not canonical rather than rewriting either:
// a rewrite is exactly what let two keys share one path. There is no leading
// slash; S3 keys never start with one.
func Compose(tenantID uuid.UUID, collection, key string) (string, error) {
	if err := ValidateCollection(collection); err != nil {
		return "", fmt.Errorf("compose storage path: collection: %w", err)
	}
	if err := ValidateKey(key); err != nil {
		return "", fmt.Errorf("compose storage path: key: %w", err)
	}
	return tenantID.String() + Separator + CollectionSegment(collection) + Separator + key, nil
}
