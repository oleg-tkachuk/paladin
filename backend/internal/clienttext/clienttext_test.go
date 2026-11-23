package clienttext

import (
	"testing"
	"unicode"
	"unicode/utf8"
)

func TestPrintable(t *testing.T) {
	for in, want := range map[string]string{
		"":                 "",
		"plain text":       "plain text",
		"at: '\x7f'":       `at: '\x7f'`,
		"a\nb\tc":          `a\nb\tc`,
		"bad \xff":         `bad \xff`,
		"arrow → ok":       "arrow → ok",
		"bidi \u202e":      `bidi \u202e`,
		"bell \a":          `bell \a`,
		"escape \x1b[31m":  `escape \x1b[31m`,
		"quote \" and \\ ": "quote \" and \\ ",
	} {
		if got := Printable(in); got != want {
			t.Errorf("Printable(%q) = %q, want %q", in, got, want)
		}
	}
}

// Whatever it is given, Printable answers one line of valid UTF-8 that
// prints, and leaves text that already prints alone.
func FuzzPrintable(f *testing.F) {
	for _, s := range []string{"", "a", "\x00", "\xff\xfe", "a\u202eb", "→"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		got := Printable(s)
		if !utf8.ValidString(got) {
			t.Fatalf("Printable(%q) = %q, not UTF-8", s, got)
		}
		printable := true
		for _, r := range s {
			if r == utf8.RuneError || (r != ' ' && !unicode.IsPrint(r)) {
				printable = false
			}
		}
		for _, r := range got {
			if r != ' ' && !unicode.IsPrint(r) {
				t.Fatalf("Printable(%q) = %q holds %U", s, got, r)
			}
		}
		if printable && utf8.ValidString(s) && got != s {
			t.Fatalf("Printable(%q) = %q, want it unchanged", s, got)
		}
	})
}
