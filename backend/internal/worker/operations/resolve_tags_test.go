package operations

import (
	"reflect"
	"testing"
)

// The executor replaced unconditionally while the request advertised a
// `replace` flag defaulting to false — so the DEFAULT batch tag update deleted
// every tag the caller did not restate. That is the case worth pinning: not
// that replace works, but that NOT asking for it leaves the rest alone.
func TestResolveTags(t *testing.T) {
	t.Parallel()

	current := map[string]string{"env": "prod", "team": "data", "keep": "me"}

	tests := []struct {
		name     string
		supplied map[string]string
		replace  bool
		want     map[string]string
	}{
		{
			name:     "merge keeps unmentioned keys",
			supplied: map[string]string{"env": "staging"},
			want:     map[string]string{"env": "staging", "team": "data", "keep": "me"},
		},
		{
			name:     "merge adds new keys",
			supplied: map[string]string{"new": "value"},
			want:     map[string]string{"env": "prod", "team": "data", "keep": "me", "new": "value"},
		},
		{
			name:     "merge with nothing supplied is a no-op",
			supplied: map[string]string{},
			want:     map[string]string{"env": "prod", "team": "data", "keep": "me"},
		},
		{
			name:     "replace drops what is not supplied",
			supplied: map[string]string{"only": "this"},
			replace:  true,
			want:     map[string]string{"only": "this"},
		},
		{
			name:     "replace with an empty map clears",
			supplied: map[string]string{},
			replace:  true,
			want:     map[string]string{},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// A fresh copy per case: the merge branch must not write into the
			// row it read, and sharing `current` between subtests would hide
			// that.
			cur := map[string]string{}
			for k, v := range current {
				cur[k] = v
			}
			got := resolveTags(cur, tc.supplied, tc.replace)
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("resolveTags = %v, want %v", got, tc.want)
			}
			if !tc.replace && !reflect.DeepEqual(cur, current) {
				t.Errorf("merge mutated the object's own tag map: %v", cur)
			}
		})
	}
}
