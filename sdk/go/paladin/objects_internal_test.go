package paladin

import (
	"errors"
	"testing"
)

// A server that names no part count leaves the split to the client, which
// used to narrow it to int32 unchecked: a size past MaxParts parts wrapped.
func TestPartCount(t *testing.T) {
	const partSize = 8 << 20
	cases := []struct {
		name    string
		size    int64
		want    int32
		wantErr bool
	}{
		{"one byte", 1, 1, false},
		{"exactly one part", partSize, 1, false},
		{"a byte over one part", partSize + 1, 2, false},
		{"exactly MaxParts parts", MaxParts * partSize, MaxParts, false},
		{"a byte over MaxParts parts", MaxParts*partSize + 1, 0, true},
		{"enough parts to wrap int32", (1 << 32) * partSize, 0, true},
		{"no bytes, no split", 0, 0, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := partCount(c.size, partSize)
			if c.wantErr {
				if !errors.Is(err, ErrTooManyParts) {
					t.Fatalf("partCount(%d) = %d, %v; want ErrTooManyParts", c.size, got, err)
				}
				return
			}
			if err != nil || got != c.want {
				t.Fatalf("partCount(%d) = %d, %v; want %d", c.size, got, err, c.want)
			}
		})
	}
}
