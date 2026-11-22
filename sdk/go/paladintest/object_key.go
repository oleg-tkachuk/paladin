package paladintest

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The server's object key rule (sdk/testdata/object_keys.json): at most
// maxKeyBytes bytes of UTF-8 without control characters, in segments of at
// most maxSegmentBytes separated by "/", none of them empty, "." or "..".
const (
	keySeparator   = "/"
	currentSegment = "."
	parentSegment  = ".."
	// maxKeyBytes is the server's storagepath.MaxKeyBytes: S3's 1024-byte path
	// less the tenant id, the longest collection segment and two separators.
	maxKeyBytes = 731
	// maxSegmentBytes is the server's storagepath.MaxSegmentBytes: the longest
	// file name a filer-backed store takes.
	maxSegmentBytes = 255
)

var keyRule = fmt.Sprintf(`an object key is at most %d bytes of UTF-8 without control characters, in segments of at most %d bytes separated by "/", none of them empty, "." or ".."`, maxKeyBytes, maxSegmentBytes)

// keyRefusal is why the server refuses key where an object is created, or ""
// when it stores an object under it.
func keyRefusal(key string) string {
	if reason := keyTextRefusal(key); reason != "" {
		return reason
	}
	switch {
	case strings.HasPrefix(key, keySeparator):
		return `it starts with "/"`
	case strings.HasSuffix(key, keySeparator):
		return `it ends with "/"`
	}
	for _, segment := range strings.Split(key, keySeparator) {
		if reason := segmentRefusal(segment); reason != "" {
			return reason
		}
	}
	return ""
}

// keyTextRefusal is why key is not key text, or "".
func keyTextRefusal(key string) string {
	switch {
	case !utf8.ValidString(key):
		return "it is not valid UTF-8"
	case len(key) > maxKeyBytes:
		return fmt.Sprintf("it is %d bytes, over the limit of %d", len(key), maxKeyBytes)
	case strings.ContainsFunc(key, unicode.IsControl):
		return "it holds a control character"
	}
	return ""
}

// segmentRefusal is why segment may not be a key segment, or "".
func segmentRefusal(segment string) string {
	switch segment {
	case "":
		return "it holds an empty segment"
	case currentSegment:
		return `it holds a "." segment`
	case parentSegment:
		return `it holds a ".." segment`
	}
	if len(segment) > maxSegmentBytes {
		return fmt.Sprintf("it holds a segment of %d bytes, over the limit of %d", len(segment), maxSegmentBytes)
	}
	return ""
}
