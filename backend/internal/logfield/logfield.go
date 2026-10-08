// Package logfield builds log fields for values that may carry a credential.
package logfield

import (
	"net/url"

	"go.uber.org/zap"
)

// unparseableURL stands in for a URL that does not parse: its text cannot be
// told apart from a credential, so none of it is logged.
const unparseableURL = "<unparseable url>"

// redactedQueryValue replaces every query parameter's value.
const redactedQueryValue = "REDACTED"

// URL logs raw with every credential-bearing part replaced: the userinfo
// password, as url.URL.Redacted does, and every query value. A broker URL
// such as amqp://user:pass@host carries a password in the userinfo; a
// presigned URL carries its signature, credential and session token in the
// query, and is a working bearer credential until it expires. Parameter
// names stay, so a log line still says what kind of URL it was. The fragment
// is dropped.
func URL(key, raw string) zap.Field {
	return zap.String(key, RedactURL(raw))
}

// RedactURL is raw as URL logs it, for a URL shown anywhere but a log line —
// a health message, say.
func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return unparseableURL
	}
	if u.RawQuery != "" {
		q := u.Query()
		for k := range q {
			q[k] = []string{redactedQueryValue}
		}
		u.RawQuery = q.Encode()
	}
	u.Fragment, u.RawFragment = "", ""
	return u.Redacted()
}
