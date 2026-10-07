package middleware

import (
	"net/http"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// ServerVersion stamps every response of next with the server's release in
// paladin.HeaderServerVersion — an error included, and the 404 for a
// procedure this release does not serve, which a Connect client reads as
// Unimplemented: that is a contract skew, and the SDK names both sides of
// it. A build without a version stamps nothing.
func ServerVersion(version string, next http.Handler) http.Handler {
	if version == "" {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set(paladin.HeaderServerVersion, version)
		next.ServeHTTP(w, r)
	})
}

// UnknownProcedurePattern is where a plane's mux mounts UnknownProcedure:
// the catch-all, which every other pattern outranks.
const UnknownProcedurePattern = "/"

// UnknownProcedure answers a request for a procedure the mux does not serve
// with a Connect Unimplemented error in the caller's protocol, rather than
// net/http's plain 404. A client then gets the error with its response
// headers — the server's release among them — where a bare 404 leaves it
// only a status. Mount it at UnknownProcedurePattern on a plane's mux; anything that is not an
// RPC still gets a 404.
func UnknownProcedure() http.Handler {
	writer := connecthttp.NewErrorWriter()
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !writer.IsSupported(r) {
			http.NotFound(w, r)
			return
		}
		_ = writer.Write(w, r, connect.Errorf(connect.CodeUnimplemented,
			"%s is not served by this release", r.URL.Path))
	})
}
