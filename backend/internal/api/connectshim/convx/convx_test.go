package convx

import (
	"testing"
	"time"
)

// ParseRV replaced three per-plane copies, one of which — iam's hand-rolled
// digit loop — overflowed silently and disagreed with the other two about
// negatives. Those two cases lead here because they are the reason this
// package exists, not because integer parsing needs proving.
func TestParseRV(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		in      string
		want    int64
		wantErr bool
	}{
		{name: "empty is the absent guard", in: "", want: 0},
		{name: "zero", in: "0", want: 0},
		{name: "one", in: "1", want: 1},
		{name: "max int64", in: "9223372036854775807", want: 9223372036854775807},
		{
			// The iam copy returned -9223372036854775808 here, with no error:
			// a guard the caller never sent, silently manufactured.
			name:    "one past max int64 is an error, not a wrapped value",
			in:      "9223372036854775808",
			wantErr: true,
		},
		{
			name:    "far past max int64",
			in:      "99999999999999999999",
			wantErr: true,
		},
		{
			// admin/data accepted this via ParseInt; iam rejected it. Neither
			// is obviously right, but they must agree — ParseInt's behaviour
			// wins because a negative version can only come from a caller
			// that made it up, and the handlers compare it and refuse.
			name: "negative parses; the version compare rejects it downstream",
			in:   "-1",
			want: -1,
		},
		{name: "not a number", in: "abc", wantErr: true},
		{name: "trailing garbage", in: "12x", wantErr: true},
		{name: "whitespace is not trimmed", in: " 12", wantErr: true},
		{name: "float is not an integer", in: "1.5", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseRV(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseRV(%q) = %d, want an error", tc.in, got)
				}
				if got != 0 {
					t.Errorf("ParseRV(%q) returned %d alongside its error; callers "+
						"that ignore the error would use it", tc.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseRV(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("ParseRV(%q) = %d, want %d", tc.in, got, tc.want)
			}
		})
	}
}

// TestResourceVersionRoundTrip pins the pair: whatever ResourceVersion emits,
// ParseRV must read back unchanged. The zero/"" mapping is the one that
// matters — it is how "no guard" travels.
func TestResourceVersionRoundTrip(t *testing.T) {
	t.Parallel()

	for _, v := range []int64{0, 1, 42, 9223372036854775807} {
		s := ResourceVersion(v)
		back, err := ParseRV(s)
		if err != nil {
			t.Fatalf("ParseRV(ResourceVersion(%d)) = %v", v, err)
		}
		if back != v {
			t.Errorf("round trip of %d produced %d (wire form %q)", v, back, s)
		}
	}
	if got := ResourceVersion(0); got != "" {
		t.Errorf("ResourceVersion(0) = %q, want \"\" — the absent-guard form", got)
	}
}

func TestPageResponseProto(t *testing.T) {
	t.Parallel()

	// A final page must carry no page field at all. Returning an empty token
	// invites a client to request one more page forever.
	if got := PageResponseProto(""); got != nil {
		t.Errorf("PageResponseProto(\"\") = %v, want nil", got)
	}
	got := PageResponseProto("cursor-1")
	if got == nil || got.GetNextPageToken() != "cursor-1" {
		t.Errorf("PageResponseProto(\"cursor-1\") = %v", got)
	}
}

func TestTsProto(t *testing.T) {
	t.Parallel()

	// nil for the zero time keeps "unset" distinguishable from "the epoch",
	// which a naive conversion would collapse.
	if got := TsProto(time.Time{}); got != nil {
		t.Errorf("TsProto(zero) = %v, want nil", got)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if got := TsProto(now); got == nil || !got.AsTime().Equal(now) {
		t.Errorf("TsProto(%v) = %v", now, got)
	}
}

func TestTsPtrProto(t *testing.T) {
	t.Parallel()

	if got := TsPtrProto(nil); got != nil {
		t.Errorf("TsPtrProto(nil) = %v, want nil", got)
	}
	zero := time.Time{}
	if got := TsPtrProto(&zero); got != nil {
		t.Errorf("TsPtrProto(&zero) = %v, want nil", got)
	}
	now := time.Now().UTC().Truncate(time.Microsecond)
	if got := TsPtrProto(&now); got == nil || !got.AsTime().Equal(now) {
		t.Errorf("TsPtrProto(%v) = %v", now, got)
	}
}
