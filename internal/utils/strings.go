package utils

import (
	"fmt"

	"github.com/dustin/go-humanize"
)

func ParseSizeString(s string) (int64, error) {
	b, err := humanize.ParseBytes(s)
	if err != nil {
		return 0, fmt.Errorf("failed to parse size %q: %w", s, err)
	}

	return int64(b), nil
}
