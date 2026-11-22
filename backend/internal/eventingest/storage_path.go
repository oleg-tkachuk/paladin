package eventingest

import (
	"strings"

	"github.com/oleg-tkachuk/paladin/backend/internal/storage/storagepath"
)

// storagePathParts is how many parts a bucket path splits into:
// tenant, collection segment, key.
const storagePathParts = 3

// splitStoragePath splits a path inside a bucket — the layout
// storagepath.Compose writes, "<tenant>/<collection segment>/<key...>" — into
// its tenant, collection and key. The collection occupies exactly one segment,
// so the split is exact: a nested collection's segment decodes to its name
// (invoices.2026.q1 → invoices/2026/q1), and the rest is the key whole. A path
// whose collection segment no collection occupies is no Paladin object.
func splitStoragePath(path string) (tenant, collection, key string, ok bool) {
	parts := strings.SplitN(path, storagepath.Separator, storagePathParts)
	if len(parts) < storagePathParts || parts[0] == "" || parts[2] == "" {
		return "", "", "", false
	}
	collection, err := storagepath.ParseCollectionSegment(parts[1])
	if err != nil {
		return "", "", "", false
	}
	return parts[0], collection, parts[2], true
}
