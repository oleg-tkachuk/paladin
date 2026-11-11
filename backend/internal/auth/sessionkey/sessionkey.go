// Package sessionkey holds the key that signs user sessions (ADR-0047): an
// ECDSA P-256 private key, read from a PEM file — PKCS#8, as openssl writes
// it, or SEC1, as Helm's genPrivateKey does — and named by its
// RFC 7638 thumbprint, and the public keys verifiers accept — its own and any
// previous ones still published after a rotation.
//
// Only the role that mints sessions loads a Set; every other role verifies
// against the JWKS that Set serves.
package sessionkey

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
)

// WellKnownPath is where the role that mints serves the session JWKS, on the
// IAM plane (RFC 8615, the path RFC 8414 names for jwks_uri by convention).
const WellKnownPath = "/.well-known/jwks.json"

// MaxAge is how long a verifier may cache the published key set: short enough
// that a rotation reaches every verifier within one release window.
const MaxAge = 5 * time.Minute

// The PEM block types Load reads.
const (
	privateKeyBlock   = "PRIVATE KEY"
	ecPrivateKeyBlock = "EC PRIVATE KEY"
	publicKeyBlock    = "PUBLIC KEY"
)

// Set is the active signing key and the public keys verifiers accept.
type Set struct {
	signer jwk.Key
	kid    string
	public jwk.Set
}

// Load reads the active private key from activePath and the public keys of
// previous ones from previousPaths. Every key must be ECDSA P-256.
func Load(activePath string, previousPaths []string) (*Set, error) {
	active, err := readPrivate(activePath)
	if err != nil {
		return nil, err
	}
	previous := make([]*ecdsa.PublicKey, 0, len(previousPaths))
	for _, path := range previousPaths {
		pub, err := readPublic(path)
		if err != nil {
			return nil, err
		}
		previous = append(previous, pub)
	}
	return FromKeys(active, previous...)
}

// FromKeys is a Set signing with active and publishing it with previous.
func FromKeys(active *ecdsa.PrivateKey, previous ...*ecdsa.PublicKey) (*Set, error) {
	if !isP256(&active.PublicKey) {
		return nil, errors.New("sessionkey: the active key is not ECDSA P-256")
	}
	signer, err := jwk.Import(active)
	if err != nil {
		return nil, fmt.Errorf("sessionkey: active key: %w", err)
	}
	kid, err := describe(signer)
	if err != nil {
		return nil, err
	}
	public := jwk.NewSet()
	for _, pub := range append([]*ecdsa.PublicKey{&active.PublicKey}, previous...) {
		if !isP256(pub) {
			return nil, errors.New("sessionkey: a previous key is not ECDSA P-256")
		}
		k, err := jwk.Import(pub)
		if err != nil {
			return nil, fmt.Errorf("sessionkey: public key: %w", err)
		}
		if _, err := describe(k); err != nil {
			return nil, err
		}
		if err := public.AddKey(k); err != nil {
			return nil, fmt.Errorf("sessionkey: public key: %w", err)
		}
	}
	return &Set{signer: signer, kid: kid, public: public}, nil
}

// Signer is the active private key, carrying its kid and ES256.
func (s *Set) Signer() jwk.Key { return s.signer }

// KeyID is the active key's kid.
func (s *Set) KeyID() string { return s.kid }

// Public is the set verifiers accept: the active public key and the previous
// ones.
func (s *Set) Public() jwk.Set { return s.public }

// Handler serves Public as a JWKS document. It is unauthenticated: public
// keys are not secrets.
func (s *Set) Handler() http.Handler {
	body, err := json.Marshal(s.public)
	if err != nil {
		// A set FromKeys built always marshals; a failure is a programming
		// error, not a runtime path.
		panic(fmt.Sprintf("sessionkey: marshal the key set: %v", err))
	}
	cacheControl := "public, max-age=" + strconv.Itoa(int(MaxAge/time.Second))
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/jwk-set+json")
		w.Header().Set("Cache-Control", cacheControl)
		_, _ = w.Write(body)
	})
}

// describe names k by its RFC 7638 thumbprint and marks it ES256, for
// signatures, returning the kid.
func describe(k jwk.Key) (string, error) {
	tp, err := k.Thumbprint(crypto.SHA256)
	if err != nil {
		return "", fmt.Errorf("sessionkey: thumbprint: %w", err)
	}
	kid := base64.RawURLEncoding.EncodeToString(tp)
	for name, value := range map[string]any{
		jwk.KeyIDKey:     kid,
		jwk.AlgorithmKey: jwa.ES256(),
		jwk.KeyUsageKey:  jwk.ForSignature,
	} {
		if err := k.Set(name, value); err != nil {
			return "", fmt.Errorf("sessionkey: %s: %w", name, err)
		}
	}
	return kid, nil
}

func isP256(pub *ecdsa.PublicKey) bool { return pub.Curve == elliptic.P256() }

func readBlock(path string) (*pem.Block, error) {
	raw, err := os.ReadFile(path) // #nosec G304 — operator-supplied path
	if err != nil {
		return nil, fmt.Errorf("sessionkey: %w", err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return nil, fmt.Errorf("sessionkey: %s: not PEM", path)
	}
	return block, nil
}

func readPrivate(path string) (*ecdsa.PrivateKey, error) {
	block, err := readBlock(path)
	if err != nil {
		return nil, err
	}
	var key any
	switch block.Type {
	case privateKeyBlock:
		key, err = x509.ParsePKCS8PrivateKey(block.Bytes)
	case ecPrivateKeyBlock:
		key, err = x509.ParseECPrivateKey(block.Bytes)
	default:
		return nil, fmt.Errorf("sessionkey: %s: a PEM %q block, not a private key", path, block.Type)
	}
	if err != nil {
		return nil, fmt.Errorf("sessionkey: %s: %w", path, err)
	}
	ec, ok := key.(*ecdsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("sessionkey: %s: a %T, not an ECDSA key", path, key)
	}
	return ec, nil
}

func readPublic(path string) (*ecdsa.PublicKey, error) {
	block, err := readBlock(path)
	if err != nil {
		return nil, err
	}
	if block.Type != publicKeyBlock {
		return nil, fmt.Errorf("sessionkey: %s: a PEM %q block, not a public key", path, block.Type)
	}
	key, err := x509.ParsePKIXPublicKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("sessionkey: %s: %w", path, err)
	}
	ec, ok := key.(*ecdsa.PublicKey)
	if !ok {
		return nil, fmt.Errorf("sessionkey: %s: a %T, not an ECDSA key", path, key)
	}
	return ec, nil
}
