package utils

import (
	"fmt"
	"strings"

	"github.com/dustin/go-humanize"
	"github.com/oleg-tkachuk/paladin/internal/safecast"
)

func ParseSizeString(s string) (int64, error) {
	b, err := humanize.ParseBytes(s)
	if err != nil {
		return 0, fmt.Errorf("failed to parse size %q: %w", s, err)
	}

	return safecast.Int64(b), nil
}

// NormalizeETag removes leading and trailing double quotes from an ETag.
func NormalizeETag(etag string) string {
	return strings.Trim(etag, "\"")
}
