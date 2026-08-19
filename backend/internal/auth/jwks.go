package auth

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// JWKSVerifier validates JWTs against a remote JWK Set. The set is fetched
// once on first use and refreshed in the background per `RefreshInterval`
// (default 1h). Verification dispatches on the token header's `kid`; an
// unknown kid triggers a forced refresh before rejecting.
//
// Algorithm support: RS256 (RSA), ES256 (P-256). HS-family is intentionally
// rejected because JWKS-published symmetric secrets are an anti-pattern.
type JWKSVerifier struct {
	URL              string
	HTTPClient       *http.Client
	RefreshInterval  time.Duration
	ExpectedIssuer   string
	ExpectedAudience string
	Leeway           time.Duration
	Now              func() time.Time

	// MinRefreshInterval rate-limits forced refreshes on cache miss. An
	// unauthenticated attacker can spray tokens with novel `kid` headers;
	// without this each miss triggers a synchronous outbound fetch,
	// DoSing the verifier and the IdP. Defaults to 30s.
	MinRefreshInterval time.Duration

	mu         sync.RWMutex
	keys       map[string]any // kid → *rsa.PublicKey | *ecdsa.PublicKey
	loadedAt   time.Time
	refreshErr error

	// sf collapses concurrent miss-driven refreshes into one outbound
	// fetch — a kid-spray of N parallel requests does 1 HTTP GET, not N.
	sf singleflight.Group
}

// NewJWKSVerifier constructs a verifier. Call Start(ctx) before serving
// requests so the key cache is warm — Verify() also lazy-loads on first hit
// but the first request would otherwise see latency.
func NewJWKSVerifier(url string) *JWKSVerifier {
	return &JWKSVerifier{
		URL:             url,
		HTTPClient:      &http.Client{Timeout: 5 * time.Second},
		RefreshInterval: 1 * time.Hour,
		Now:             time.Now,
		keys:            map[string]any{},
	}
}

// Start kicks off a background refresh loop. Returns immediately after
// the initial sync; subsequent ticks happen async. ctx cancels the loop.
func (v *JWKSVerifier) Start(ctx context.Context) error {
	if err := v.refresh(ctx); err != nil {
		return fmt.Errorf("jwks: initial sync: %w", err)
	}
	go v.loop(ctx)
	return nil
}

func (v *JWKSVerifier) loop(ctx context.Context) {
	if v.RefreshInterval <= 0 {
		v.RefreshInterval = 1 * time.Hour
	}
	t := time.NewTicker(v.RefreshInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = v.refresh(ctx)
		}
	}
}

// Verify implements TokenVerifier. Decodes the JWT, picks the right key by
// kid, validates the signature + standard claims, returns Principal.
func (v *JWKSVerifier) Verify(ctx context.Context, token string) (*Principal, error) {
	parts := strings.SplitN(token, ".", 3)
	if len(parts) != 3 {
		return nil, errors.New("jwt: malformed token")
	}
	headerB, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, fmt.Errorf("jwt: header decode: %w", err)
	}
	var hdr struct {
		Alg string `json:"alg"`
		Kid string `json:"kid"`
	}
	if err := json.Unmarshal(headerB, &hdr); err != nil {
		return nil, fmt.Errorf("jwt: header parse: %w", err)
	}
	if hdr.Alg == "HS256" || hdr.Alg == "HS384" || hdr.Alg == "HS512" {
		return nil, errors.New("jwt: HS-family rejected by JWKS verifier")
	}

	key, err := v.keyFor(ctx, hdr.Kid)
	if err != nil {
		return nil, err
	}

	// Reuse the existing JWTVerifier signature check by faking a thin
	// wrapper; this keeps the algorithm-dispatch in one place.
	jwtv := &JWTVerifier{
		Key:              key,
		ExpectedIssuer:   v.ExpectedIssuer,
		ExpectedAudience: v.ExpectedAudience,
		Leeway:           v.Leeway,
		Now:              v.Now,
	}
	return jwtv.Verify(ctx, token)
}

// keyFor returns the cached public key for kid; on miss it forces one
// refresh before reporting absence. The forced refresh is debounced
// (MinRefreshInterval) and singleflighted so a flood of unknown-kid
// requests can't turn into an outbound-fetch storm.
func (v *JWKSVerifier) keyFor(ctx context.Context, kid string) (any, error) {
	v.mu.RLock()
	if k, ok := v.keys[kid]; ok {
		v.mu.RUnlock()
		return k, nil
	}
	loadedAt := v.loadedAt
	v.mu.RUnlock()

	minRefresh := v.MinRefreshInterval
	if minRefresh <= 0 {
		minRefresh = 30 * time.Second
	}
	// Debounce: if we refreshed very recently, the kid is genuinely
	// unknown — don't hammer the IdP on every spray request.
	if !loadedAt.IsZero() && v.Now().Sub(loadedAt) < minRefresh {
		return nil, fmt.Errorf("jwks: unknown kid %q", kid)
	}

	// Singleflight the refresh: concurrent misses share one fetch.
	if _, err, _ := v.sf.Do("refresh", func() (any, error) {
		// Re-check under the flight: another goroutine may have just
		// refreshed (and may have moved loadedAt forward).
		v.mu.RLock()
		recent := !v.loadedAt.IsZero() && v.Now().Sub(v.loadedAt) < minRefresh
		v.mu.RUnlock()
		if recent {
			return nil, nil
		}
		return nil, v.refresh(ctx)
	}); err != nil {
		return nil, fmt.Errorf("jwks: refresh on miss: %w", err)
	}

	v.mu.RLock()
	defer v.mu.RUnlock()
	if k, ok := v.keys[kid]; ok {
		return k, nil
	}
	return nil, fmt.Errorf("jwks: unknown kid %q", kid)
}

// refresh fetches the JWK Set, parses each key, and atomically swaps the
// cache. Old keys are dropped — IdPs that overlap kids during rotation
// republish the previous one.
func (v *JWKSVerifier) refresh(ctx context.Context) error {
	if v.URL == "" {
		return errors.New("jwks: URL not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.URL, nil)
	if err != nil {
		return err
	}
	resp, err := v.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("jwks: status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20)) // 1 MiB cap
	if err != nil {
		return err
	}
	keys, err := parseJWKS(body)
	if err != nil {
		return err
	}
	v.mu.Lock()
	v.keys = keys
	v.loadedAt = v.Now()
	v.refreshErr = nil
	v.mu.Unlock()
	return nil
}

// jwk is the wire shape of a JSON Web Key (subset Paladin needs).
type jwk struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"` // "RSA" | "EC"
	Alg string `json:"alg"`
	Use string `json:"use"`
	// RSA fields
	N string `json:"n"`
	E string `json:"e"`
	// EC fields
	Crv string `json:"crv"` // "P-256" | "P-384" | "P-521"
	X   string `json:"x"`
	Y   string `json:"y"`
}

func parseJWKS(body []byte) (map[string]any, error) {
	var doc struct {
		Keys []jwk `json:"keys"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, fmt.Errorf("jwks: decode: %w", err)
	}
	out := make(map[string]any, len(doc.Keys))
	for _, k := range doc.Keys {
		if k.Use != "" && k.Use != "sig" {
			continue // signing keys only
		}
		key, err := jwkToPublicKey(k)
		if err != nil {
			// Skip unparseable keys but keep going — rotation may yield
			// transient mixed shapes; one bad key shouldn't fail the set.
			continue
		}
		if k.Kid == "" {
			continue
		}
		out[k.Kid] = key
	}
	if len(out) == 0 {
		return nil, errors.New("jwks: no usable signing keys in response")
	}
	return out, nil
}

func jwkToPublicKey(k jwk) (any, error) {
	switch k.Kty {
	case "RSA":
		nb, err := base64.RawURLEncoding.DecodeString(k.N)
		if err != nil {
			return nil, fmt.Errorf("jwks: RSA N: %w", err)
		}
		eb, err := base64.RawURLEncoding.DecodeString(k.E)
		if err != nil {
			return nil, fmt.Errorf("jwks: RSA E: %w", err)
		}
		// Exponent fits in int.
		var ev int
		for _, b := range eb {
			ev = ev<<8 | int(b)
		}
		return &rsa.PublicKey{
			N: new(big.Int).SetBytes(nb),
			E: ev,
		}, nil
	case "EC":
		var curve elliptic.Curve
		switch k.Crv {
		case "P-256":
			curve = elliptic.P256()
		case "P-384":
			curve = elliptic.P384()
		case "P-521":
			curve = elliptic.P521()
		default:
			return nil, fmt.Errorf("jwks: unsupported curve %q", k.Crv)
		}
		xb, err := base64.RawURLEncoding.DecodeString(k.X)
		if err != nil {
			return nil, fmt.Errorf("jwks: EC X: %w", err)
		}
		yb, err := base64.RawURLEncoding.DecodeString(k.Y)
		if err != nil {
			return nil, fmt.Errorf("jwks: EC Y: %w", err)
		}
		return &ecdsa.PublicKey{
			Curve: curve,
			X:     new(big.Int).SetBytes(xb),
			Y:     new(big.Int).SetBytes(yb),
		}, nil
	default:
		return nil, fmt.Errorf("jwks: unsupported kty %q", k.Kty)
	}
}
