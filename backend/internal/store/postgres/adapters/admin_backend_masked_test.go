package adapters

import "testing"

// A field outside the update mask goes to the UPDATE as nil, which its
// COALESCE reads as "keep the column"; one inside it goes as its value, the
// zero value included — clearing a field is an edit too.
func TestMasked(t *testing.T) {
	if got := masked(false, "x"); got != nil {
		t.Errorf("outside the mask = %v, want nil", *got)
	}
	if got := masked(true, ""); got == nil || *got != "" {
		t.Errorf("inside the mask, zero value = %v, want pointer to \"\"", got)
	}
	const want int64 = 1500
	if got := masked(true, want); got == nil || *got != want {
		t.Errorf("inside the mask = %v, want pointer to %d", got, want)
	}
}
