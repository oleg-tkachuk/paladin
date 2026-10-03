package objecth

import (
	"strings"
	"testing"

	"connectrpc.com/connect"
)

// content_disposition used to go to the signer as the caller sent it, so a
// presigned GET could be made to answer with any header value the caller
// chose. Only inline/attachment with a filename now reaches the URL, in
// canonical form.
func TestNormalizeContentDisposition(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string // "" with ok → no disposition
		ok   bool
	}{
		{"absent", "", "", true},
		{"attachment", "attachment", "attachment", true},
		{"inline, case folded", "INLINE", "inline", true},
		{"quoted filename", `attachment; filename="report 2026.pdf"`, `attachment; filename="report 2026.pdf"`, true},
		{"bare token filename gains no quotes it does not need", "attachment; filename=report.pdf", "attachment; filename=report.pdf", true},
		{"non-ASCII filename is RFC 2231 encoded", `attachment; filename*=UTF-8''%D0%B7%D0%B2%D1%96%D1%82.pdf`, "attachment; filename*=utf-8''%D0%B7%D0%B2%D1%96%D1%82.pdf", true},
		{"another type", "form-data; name=x", "", false},
		{"an unknown parameter", `attachment; filename="a"; creation-date="x"`, "", false},
		{"header injection", "attachment\r\nSet-Cookie: s=1", "", false},
		{"unparseable", "attachment; filename=", "", false},
		{"path in filename", `attachment; filename="../../etc/passwd"`, "", false},
		{"windows path in filename", `attachment; filename="C:\\x.exe"`, "", false},
		{"too long", "attachment; filename=" + strings.Repeat("a", maxContentDispositionBytes), "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NormalizeContentDisposition(tc.in)
			if !tc.ok {
				if connect.CodeOf(err) != connect.CodeInvalidArgument {
					t.Fatalf("NormalizeContentDisposition(%q) = %q, %v; want InvalidArgument", tc.in, got, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got != tc.want {
				t.Fatalf("NormalizeContentDisposition(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}
