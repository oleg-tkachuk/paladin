// Package clienttext makes text a parser wrote safe to hand a client.
//
// A parser names the token it could not read as its input holds it, so a
// control character, a terminal escape or a byte that is not UTF-8 in a
// request comes back raw inside the refusal. Printable escapes them the way a
// Go string literal does, leaving everything that prints as it is.
package clienttext

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Printable is s with each character that does not print, and each byte that
// is not UTF-8, escaped as a Go string literal escapes it. A space is kept;
// a newline or a tab is escaped, so the result is one line.
func Printable(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		switch {
		case r == utf8.RuneError && size == 1:
			fmt.Fprintf(&b, `\x%02x`, s[i])
		case r == ' ' || unicode.IsPrint(r):
			b.WriteRune(r)
		default:
			q := strconv.QuoteRune(r)
			b.WriteString(q[1 : len(q)-1])
		}
		i += size
	}
	return b.String()
}
