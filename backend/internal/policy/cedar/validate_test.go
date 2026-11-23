package cedar

import (
	"strings"
	"testing"
	"unicode"
)

// A parse error names the position in the operator's own text. Parsed behind
// the built-in layer, the first line of the text was reported at the line
// after the last built-in one.
func TestValidateNamesThePositionInTheText(t *testing.T) {
	err := Validate("permit(principal, action, resource);\nnope")
	if err == nil {
		t.Fatal("Validate accepted text that does not parse")
	}
	if !strings.Contains(err.Error(), ":2:") {
		t.Errorf("error = %q, want the position on line 2 of the text", err)
	}
}

// The parser echoes a token it cannot read as the text holds it; what the
// client reads has it escaped.
func TestValidateEscapesWhatDoesNotPrint(t *testing.T) {
	for _, text := range []string{"\a", "\x1b[31m", "permit(\x00", "\xff"} {
		err := Validate(text)
		if err == nil {
			t.Fatalf("Validate(%q) accepted it", text)
		}
		for _, r := range err.Error() {
			if r != ' ' && !unicode.IsPrint(r) {
				t.Errorf("Validate(%q) = %q holds unprintable %U", text, err, r)
			}
		}
	}
}

func TestValidateAcceptsAPolicy(t *testing.T) {
	for _, text := range []string{"", "permit(principal, action, resource);"} {
		if err := Validate(text); err != nil {
			t.Errorf("Validate(%q) = %v", text, err)
		}
	}
}
