// Package bytesize parses the human-readable sizes used in configuration
// ("5MiB", "100 MB", "1GB").
package bytesize

import (
	"fmt"

	"github.com/dustin/go-humanize"

	"github.com/oleg-tkachuk/paladin/backend/internal/safecast"
)

// Parse returns the number of bytes s names.
func Parse(s string) (int64, error) {
	b, err := humanize.ParseBytes(s)
	if err != nil {
		return 0, fmt.Errorf("failed to parse size %q: %w", s, err)
	}
	return safecast.Int64(b), nil
}
