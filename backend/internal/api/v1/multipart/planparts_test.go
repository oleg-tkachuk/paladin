package multipart

import "testing"

// planParts is what makes the response's `recommended_part_size` and
// `total_parts` meaningful. They were previously zero, which left a client
// with nothing to slice the file by — PresignPart then rejected every part
// number as out of range, so multipart upload did not work at all.
func TestPlanParts(t *testing.T) {
	const mib = int64(1024 * 1024)

	for _, tc := range []struct {
		name      string
		size      int64
		wantSize  int64
		wantParts int32
	}{
		{"single byte still gets one part", 1, 5 * mib, 1},
		{"exactly one minimum part", 5 * mib, 5 * mib, 1},
		{"one byte over splits in two", 5*mib + 1, 5 * mib, 2},
		{"12 MiB is three parts", 12 * mib, 5 * mib, 3},
		{
			// At the 5 MiB floor, 10 000 parts reach ~48.8 GiB. Past that
			// the size has to double rather than the count grow.
			"just under the part-count cap keeps the minimum size",
			maxPartCount * minPartSizeBytes,
			5 * mib,
			10000,
		},
		{
			"past the cap doubles the part size",
			maxPartCount*minPartSizeBytes + 1,
			10 * mib,
			5001,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			size, parts := planParts(tc.size)
			if size != tc.wantSize || parts != tc.wantParts {
				t.Errorf("planParts(%d) = (%d, %d), want (%d, %d)",
					tc.size, size, parts, tc.wantSize, tc.wantParts)
			}
			// The invariant that matters regardless of the numbers above:
			// the parts must actually cover the object, and there must not
			// be more of them than the backend accepts.
			if int64(parts)*size < tc.size {
				t.Errorf("%d parts of %d bytes do not cover %d", parts, size, tc.size)
			}
			if int64(parts) > maxPartCount {
				t.Errorf("%d parts exceeds the %d cap", parts, maxPartCount)
			}
		})
	}
}
