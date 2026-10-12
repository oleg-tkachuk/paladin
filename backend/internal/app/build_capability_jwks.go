package app

import (
	"crypto/ed25519"
	"net/http"
	"time"

	"github.com/oleg-tkachuk/limes/jwks"
)

// JWKSHandler returns an http.Handler that serves the supplied public
// key set as a JWKS document (RFC 7517 + RFC 8037). Used by the admin
// plane to expose capability-issuer public keys at
// `/.well-known/jwks.json` so verifiers in other planes / pods fetch
// and cache locally without an RPC.
//
// Cache-Control: max-age jwksMaxAge means each verifier refreshes every 5
// minutes — fast enough for rotation to propagate in a release window,
// slow enough that a thundering herd at deploy time doesn't stampede
// the admin pod.
//
// The endpoint is unauthenticated by design: public keys are not
// secrets, and federated IdP integrators expect the URL to be
// reachable without bootstrap credentials.
func JWKSHandler(publicKeys map[string]ed25519.PublicKey) http.Handler {
	h, err := jwks.NewHandler(jwks.HandlerConfig{
		Keys:   func() map[string]ed25519.PublicKey { return publicKeys },
		MaxAge: jwksMaxAge,
	})
	if err != nil {
		// Only a nil key source or a negative max-age fails, neither of
		// which this constructor passes: a programming error, not a runtime
		// path.
		panic(err)
	}
	return h
}

// jwksMaxAge is how long a verifier keeps the published key set.
const jwksMaxAge = 5 * time.Minute
