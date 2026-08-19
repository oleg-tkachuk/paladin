package s3adapter

import (
	"fmt"
	"os"
)

// rotatingTokenRetriever satisfies stscreds.IdentityTokenRetriever and
// reads the file on every call. Replaces the SDK's stock
// stscreds.IdentityTokenFile because that helper, while it does re-read,
// returns a typed wrapper that's hard to test against — and any future
// switch to embedded-string token sources would silently regress to a
// captured value. This explicit type makes the rotation contract a
// first-class part of Paladin's adapter surface.
//
// EKS IRSA writes the token file via an atomic-rename pattern (the kubelet
// rewrites the projected service-account token roughly every 80% of the
// configured TTL — typical default is 1 hour, so rewrite ~48 minutes).
// os.ReadFile follows the path each call, so a long-lived pod whose token
// rotates picks up the new value at the next STS AssumeRoleWithWebIdentity
// invocation. The aws.CredentialsCache layer caches the *STS-issued*
// credentials, not the JWT — so the SDK calls back here whenever the cache
// expires, which is what triggers the fresh file read.
type rotatingTokenRetriever struct {
	path string
}

// GetIdentityToken implements stscreds.IdentityTokenRetriever.
func (r *rotatingTokenRetriever) GetIdentityToken() ([]byte, error) {
	b, err := os.ReadFile(r.path) // #nosec G304 — operator-supplied path
	if err != nil {
		return nil, fmt.Errorf("read web identity token %q: %w", r.path, err)
	}
	return b, nil
}

// Compile-time check that the AWS SDK accepts our retriever. The interface
// is intentionally narrow — one method — so this prevents drift if AWS
// changes the contract in a future SDK release.
var _ interface {
	GetIdentityToken() ([]byte, error)
} = (*rotatingTokenRetriever)(nil)
