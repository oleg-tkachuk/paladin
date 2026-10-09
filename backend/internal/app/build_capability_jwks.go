package app

import (
	"crypto/ed25519"
	"net/http"

	"github.com/oleg-tkachuk/limes"
)

// JWKSHandler returns an http.Handler that serves the supplied public
// key set as a JWKS document (RFC 7517 + RFC 8037). Used by the admin
// plane to expose capability-issuer public keys at
// `/.well-known/jwks.json` so verifiers in other planes / pods fetch
// and cache locally without an RPC.
//
// Cache-Control: max-age 300 means each verifier refreshes every 5
// minutes — fast enough for rotation to propagate in a release window,
// slow enough that a thundering herd at deploy time doesn't stampede
// the admin pod.
//
// The endpoint is unauthenticated by design: public keys are not
// secrets, and federated IdP integrators expect the URL to be
// reachable without bootstrap credentials.
func JWKSHandler(publicKeys map[string]ed25519.PublicKey) http.Handler {
	// Marshal once at construction time. Rotation pushes a new map
	// pointer to a fresh handler so we never lock per-request — when
	// rotation lands it'll wrap this in an atomic.Value swap.
	body, err := limes.MarshalJWKS(publicKeys)
	if err != nil {
		// At construction this only fails if the map holds a non-Ed25519
		// key (which the bundle constructor never produces). Panicking
		// is correct here: it's a programming error, not a runtime path.
		panic(err)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/jwk-set+json")
		w.Header().Set("Cache-Control", "public, max-age=300")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(body)
	})
}
