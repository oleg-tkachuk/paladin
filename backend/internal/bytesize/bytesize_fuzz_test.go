package bytesize

import "testing"

// Any size an operator writes: refused, or a byte count that is not negative.
// A size past int64 is clamped, never wrapped into a negative limit.
func FuzzParse(f *testing.F) {
	for _, s := range []string{"1KiB", "1MiB", "1GiB", "1000B", "100 MB", "invalid", "-1", "20EB", "1e30", ""} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		n, err := Parse(s)
		if err == nil && n < 0 {
			t.Fatalf("Parse(%q) = %d", s, n)
		}
	})
}
