// Package logfield builds log fields for values that may carry a credential.
package logfield

import (
	"net/url"

	"go.uber.org/zap"
)

// unparseableURL stands in for a URL that does not parse: its text cannot be
// told apart from a credential, so none of it is logged.
const unparseableURL = "<unparseable url>"

// URL logs raw with the userinfo password replaced, as url.URL.Redacted does:
// a broker URL such as amqp://user:pass@host carries the password in the
// string, and a log line is no place for it.
func URL(key, raw string) zap.Field {
	u, err := url.Parse(raw)
	if err != nil {
		return zap.String(key, unparseableURL)
	}
	return zap.String(key, u.Redacted())
}
