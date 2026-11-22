package paladintest

import "strings"

// The server's object key rule (sdk/testdata/object_keys.json): segments
// separated by "/", none of them empty, "." or "..".
const (
	keySeparator   = "/"
	currentSegment = "."
	parentSegment  = ".."
	keyRule        = `an object key is segments separated by "/", none of them empty, "." or ".."`
)

// keyRefusal is why the server refuses key where an object is created, or ""
// when it stores an object under it.
func keyRefusal(key string) string {
	switch {
	case strings.HasPrefix(key, keySeparator):
		return `it starts with "/"`
	case strings.HasSuffix(key, keySeparator):
		return `it ends with "/"`
	}
	for _, segment := range strings.Split(key, keySeparator) {
		switch segment {
		case "":
			return "it holds an empty segment"
		case currentSegment:
			return `it holds a "." segment`
		case parentSegment:
			return `it holds a ".." segment`
		}
	}
	return ""
}
