package iam

import (
	"strings"
	"testing"
	"uuid"
)

// Any user resource name: refused, or users/{id} or users/{id}/settings with
// nothing after it — the whole of what follows users/ names the user, so a
// name with more in it does not address the user its first segment does.
func FuzzParseUserResourceName(f *testing.F) {
	id := uuid.MustParse("7d1e2f3a-0b4c-4d5e-8f60-718293a4b5c6")
	for _, s := range []string{
		"users/" + id.String(),
		"users/" + id.String() + "/settings",
		"users/",
		"people/" + id.String(),
		"users/nope",
		"",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, name string) {
		got, err := parseUserResourceName(name)
		if err != nil {
			return
		}
		body := strings.TrimSuffix(strings.TrimPrefix(name, userNamePrefix), userSettingsSuffix)
		want, perr := uuid.Parse(body)
		if perr != nil || want != got {
			t.Fatalf("%q parses to user %s, but %q is not that user's id", name, got, body)
		}
	})
}
