package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"

	"github.com/lestrrat-go/jwx/v3/jwa"
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v3/jws"
	"github.com/lestrrat-go/jwx/v3/jwt"
	"github.com/oleg-tkachuk/paladin/backend/internal/logfield"
	"go.uber.org/zap"

	"golang.org/x/sync/singleflight"
)

// JWKSVerifier validates JWTs against a remote JWK Set. The set is fetched
// once on first use and refreshed in the background per `RefreshInterval`
// (default 1h). Verification dispatches on the token header's `kid`; an
// unknown kid triggers a forced refresh before rejecting.
//
// The set is read and tokens are verified by jwx: RSA and EC keys, by the
// algorithm each key names or its type implies. A JWKS-published symmetric
// secret is never used, since anyone who can read the set could sign.
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

	// Logger receives a background refresh that failed: the cached keys stay
	// in use, and a key the identity provider rotated in is refused until a
	// refresh succeeds. nil keeps the failure silent.
	Logger *zap.Logger

	mu       sync.RWMutex
	keys     jwk.Set // the public signing keys, by kid
	loadedAt time.Time

	// sf collapses concurrent miss-driven refreshes into one outbound
	// fetch — a kid-spray of N parallel requests does 1 HTTP GET, not N.
	sf singleflight.Group
}

// What NewJWKSVerifier and a refresh fall back to.
const (
	defaultJWKSFetchTimeout   = 5 * time.Second
	defaultRefreshInterval    = time.Hour
	defaultMinRefreshInterval = 30 * time.Second
	// maxJWKSBytes bounds the JWK Set read from the identity provider.
	maxJWKSBytes = 1 << 20
)

// NewJWKSVerifier constructs a verifier. Call Start(ctx) before serving
// requests so the key cache is warm — Verify() also lazy-loads on first hit
// but the first request would otherwise see latency.
func NewJWKSVerifier(url string) *JWKSVerifier {
	return &JWKSVerifier{
		URL:             url,
		HTTPClient:      &http.Client{Timeout: defaultJWKSFetchTimeout},
		RefreshInterval: defaultRefreshInterval,
		Now:             time.Now,
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
	t := time.NewTicker(refreshEvery(v.RefreshInterval))
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := v.refresh(ctx); err != nil && v.Logger != nil {
				v.Logger.Warn("jwks refresh failed; keeping the cached keys",
					logfield.URL("url", v.URL), zap.Error(err))
			}
		}
	}
}

// refreshEvery is how often the set is fetched again: interval, or the
// default when it names none. The loop reads it once rather than writing the
// default into the verifier, which Verify reads concurrently.
func refreshEvery(interval time.Duration) time.Duration {
	if interval <= 0 {
		return defaultRefreshInterval
	}
	return interval
}

// Verify implements TokenVerifier: the signature by the key the token's kid
// names, checked by jws against the set, then the time, issuer and audience
// claims by jwt, then the Principal the claims name.
func (v *JWKSVerifier) Verify(ctx context.Context, token string) (*Principal, error) {
	msg, err := jws.ParseString(token)
	if err != nil {
		return nil, fmt.Errorf("jwt: malformed token: %w", err)
	}
	sigs := msg.Signatures()
	if len(sigs) != 1 {
		return nil, errors.New("jwt: want exactly one signature")
	}
	kid, _ := sigs[0].ProtectedHeaders().KeyID()
	set, err := v.keySetWith(ctx, kid)
	if err != nil {
		return nil, err
	}
	payload, err := jws.Verify([]byte(token), jws.WithKeySet(set, jws.WithInferAlgorithmFromKey(true)))
	if err != nil {
		return nil, fmt.Errorf("jwt: signature: %w", err)
	}
	if _, err := jwt.Parse(payload, append(v.claimChecks(), jwt.WithVerify(false))...); err != nil {
		return nil, fmt.Errorf("jwt: %w", err)
	}
	var c jwtClaims
	if err := json.Unmarshal(payload, &c); err != nil {
		return nil, fmt.Errorf("jwt: claims parse: %w", err)
	}
	return principalFromClaims(c, v.ExpectedAudience)
}

// claimChecks are the claims a token must carry to be accepted now: exp and
// nbf within the leeway, and the issuer and audience when set.
func (v *JWKSVerifier) claimChecks() []jwt.ParseOption {
	opts := []jwt.ParseOption{
		jwt.WithAcceptableSkew(v.Leeway),
		jwt.WithClock(jwt.ClockFunc(v.Now)),
	}
	if v.ExpectedIssuer != "" {
		opts = append(opts, jwt.WithIssuer(v.ExpectedIssuer))
	}
	if v.ExpectedAudience != "" {
		opts = append(opts, jwt.WithAudience(v.ExpectedAudience))
	}
	return opts
}

// keySetWith returns the cached key set once it holds kid; on a miss it
// forces one refresh before reporting absence. The forced refresh is
// debounced (MinRefreshInterval) and singleflighted so a flood of unknown-kid
// requests can't turn into an outbound-fetch storm.
func (v *JWKSVerifier) keySetWith(ctx context.Context, kid string) (jwk.Set, error) {
	v.mu.RLock()
	set, loadedAt := v.keys, v.loadedAt
	v.mu.RUnlock()
	if holds(set, kid) {
		return set, nil
	}

	minRefresh := v.MinRefreshInterval
	if minRefresh <= 0 {
		minRefresh = defaultMinRefreshInterval
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
	if holds(v.keys, kid) {
		return v.keys, nil
	}
	return nil, fmt.Errorf("jwks: unknown kid %q", kid)
}

// holds reports whether set has a key under kid; a token without a kid
// matches none.
func holds(set jwk.Set, kid string) bool {
	if set == nil || kid == "" {
		return false
	}
	_, ok := set.LookupKeyID(kid)
	return ok
}

// refresh fetches the JWK Set, keeps its signing keys, and atomically swaps
// the cache. Old keys are dropped — IdPs that overlap kids during rotation
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
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxJWKSBytes))
	if err != nil {
		return err
	}
	keys, err := signingKeys(body)
	if err != nil {
		return err
	}
	v.mu.Lock()
	v.keys = keys
	v.loadedAt = v.Now()
	v.mu.Unlock()
	return nil
}

// signingKeys is the set's public signing keys: RSA or EC, for signatures,
// under a kid. A shared secret is left out — a JWKS-published symmetric key is
// an anti-pattern, and it would let anyone holding the set sign — as are
// encryption keys, keys jwk could not read, and keys no token could name.
func signingKeys(body []byte) (jwk.Set, error) {
	// A key jwk cannot read is skipped rather than failing the set: an
	// identity provider mid-rotation may publish shapes it does not know.
	all, err := jwk.Parse(body, jwk.WithIgnoreParseError(true))
	if err != nil {
		return nil, fmt.Errorf("jwks: decode: %w", err)
	}
	out := jwk.NewSet()
	for i := range all.Len() {
		k, _ := all.Key(i)
		if kt := k.KeyType(); kt != jwa.RSA() && kt != jwa.EC() {
			continue
		}
		if use, ok := k.KeyUsage(); ok && use != string(jwk.ForSignature) {
			continue
		}
		if kid, ok := k.KeyID(); !ok || kid == "" {
			continue
		}
		if err := out.AddKey(k); err != nil {
			return nil, fmt.Errorf("jwks: %w", err)
		}
	}
	if out.Len() == 0 {
		return nil, errors.New("jwks: no usable signing keys in response")
	}
	return out, nil
}
