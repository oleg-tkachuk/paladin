// Package paladin is a thin client over the generated Connect stubs in gen/.
//
// It owns what every call needs and nothing else: the base URL of one plane,
// credentials, the idempotency key, and retries for calls that are safe to
// repeat. The service clients themselves are the generated ones:
//
//	c, err := paladin.New("https://admin.example.com", paladin.WithBearerToken(token))
//	tenants := paladinadminv1connect.NewTenantServiceClient(c.HTTPClient(), c.BaseURL(), c.ClientOptions()...)
package paladin

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"connectrpc.com/connect"
)

// Header names the server reads. The backend imports these, so a client and
// the server cannot spell them differently.
const (
	// HeaderAuthorization carries an API token or an OIDC JWT as a bearer
	// credential.
	HeaderAuthorization = "Authorization"
	// HeaderAPIToken is the alternative header for an API token.
	HeaderAPIToken = "X-Paladin-API-Token" // #nosec G101 -- a header name, not a credential
	// HeaderCapability carries a capability token.
	HeaderCapability = "X-Paladin-Capability"
	// HeaderIdempotencyKey makes a mutating call safe to repeat: the server
	// replays the first response for a key it has already seen.
	HeaderIdempotencyKey = "Idempotency-Key"

	// HeaderUserAgent names the SDK and its version; set it with
	// WithHeader to override.
	HeaderUserAgent = "User-Agent"
	// HeaderRetryAfter is how the server asks a client to wait, in seconds.
	HeaderRetryAfter = "Retry-After"

	bearerScheme = "Bearer"
)

// Retry defaults, used when WithRetries is given no delay.
const (
	DefaultRetryBaseDelay = 100 * time.Millisecond
	DefaultRetryMaxDelay  = 5 * time.Second
)

// Errors returned by New.
var (
	ErrEmptyBaseURL   = errors.New("paladin: base URL is empty")
	ErrInvalidBaseURL = errors.New("paladin: base URL must be an absolute http or https URL")
	ErrInvalidRetries = errors.New("paladin: retry attempts must be at least 1")
)

// Client holds what the generated service clients of one plane need.
type Client struct {
	httpClient connect.HTTPClient
	baseURL    string
	options    []connect.ClientOption
	transfer   *Transfer
}

// Option configures a Client.
type Option func(*config)

type config struct {
	httpClient connect.HTTPClient
	headers    http.Header
	retry      *retryPolicy
	tokens     *tokenAuth
	extra      []connect.ClientOption
	transfer   *Transfer
	tls        *TLS
	// httpClientSet is whether WithHTTPClient was given, which WithTLS
	// cannot be combined with.
	httpClientSet bool
	// anyPlaneTokens and audience: WithTokens, and the plane Connect builds.
	anyPlaneTokens TokenSource
	audience       string
}

// WithTransfer sends the presigned requests of Upload and Download through t:
// declared once here, it holds for every transfer through the client.
func WithTransfer(t *Transfer) Option {
	return func(cfg *config) { cfg.transfer = t }
}

// WithHTTPClient replaces http.DefaultClient.
func WithHTTPClient(c connect.HTTPClient) Option {
	return func(cfg *config) { cfg.httpClient, cfg.httpClientSet = c, true }
}

// WithBearerToken sends token as `Authorization: Bearer <token>`. An API token
// (paladin_pat_…) and an OIDC JWT are both accepted there.
func WithBearerToken(token string) Option {
	return func(cfg *config) { cfg.headers.Set(HeaderAuthorization, bearerScheme+" "+token) }
}

// WithHeader sends name: value on every call, replacing what the SDK would
// send there, the User-Agent included.
func WithHeader(name, value string) Option {
	return func(cfg *config) { cfg.headers.Set(name, value) }
}

// WithAPIToken sends an API token in HeaderAPIToken. WithBearerToken works
// for an API token too; this is for a proxy that strips Authorization.
func WithAPIToken(token string) Option {
	return func(cfg *config) { cfg.headers.Set(HeaderAPIToken, token) }
}

// WithCapability sends a capability token in HeaderCapability.
func WithCapability(token string) Option {
	return func(cfg *config) { cfg.headers.Set(HeaderCapability, token) }
}

// WithRetries retries a unary call up to attempts times in total when the
// server answers Unavailable or ResourceExhausted, and only when the call is
// safe to repeat: the RPC is declared free of side effects or idempotent, or
// the request carries an idempotency key — which every call with side
// effects does, see WithIdempotencyKey. The wait before each retry is drawn
// at random up to a ceiling that doubles from baseDelay to
// DefaultRetryMaxDelay, and is never shorter than a Retry-After the server
// sent; a retry that could not start before the context's deadline is not
// attempted. A zero baseDelay means DefaultRetryBaseDelay.
func WithRetries(attempts int, baseDelay time.Duration) Option {
	return func(cfg *config) {
		if baseDelay <= 0 {
			baseDelay = DefaultRetryBaseDelay
		}
		cfg.retry = &retryPolicy{attempts: attempts, baseDelay: baseDelay, maxDelay: DefaultRetryMaxDelay}
	}
}

// WithClientOptions passes Connect options through, e.g. connect.WithGRPC().
func WithClientOptions(opts ...connect.ClientOption) Option {
	return func(cfg *config) { cfg.extra = append(cfg.extra, opts...) }
}

// New returns a Client for the plane served at baseURL.
func New(baseURL string, opts ...Option) (*Client, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, ErrEmptyBaseURL
	}
	u, err := url.Parse(baseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return nil, fmt.Errorf("%w: %q", ErrInvalidBaseURL, baseURL)
	}

	cfg := &config{httpClient: http.DefaultClient, headers: http.Header{}}
	for _, opt := range opts {
		opt(cfg)
	}
	if cfg.retry != nil && cfg.retry.attempts < 1 {
		return nil, ErrInvalidRetries
	}
	if cfg.tls != nil {
		if cfg.httpClientSet {
			return nil, ErrTLSAndHTTP
		}
		t, err := cfg.tls.Transport()
		if err != nil {
			return nil, err
		}
		cfg.httpClient = &http.Client{Transport: t}
	}
	if cfg.anyPlaneTokens != nil && cfg.tokens == nil {
		if cfg.audience == "" {
			return nil, ErrNoAudience
		}
		cfg.tokens = &tokenAuth{source: cfg.anyPlaneTokens, audience: cfg.audience}
	}

	if cfg.headers.Get(HeaderUserAgent) == "" {
		cfg.headers.Set(HeaderUserAgent, userAgent())
	}
	interceptors := []connect.Interceptor{&headerInterceptor{headers: cfg.headers}}
	if cfg.tokens != nil {
		interceptors = append(interceptors, cfg.tokens)
	}
	if cfg.retry != nil {
		interceptors = append(interceptors, cfg.retry)
	}
	options := append([]connect.ClientOption{connect.WithInterceptors(interceptors...)}, cfg.extra...)

	return &Client{
		httpClient: cfg.httpClient,
		baseURL:    strings.TrimRight(baseURL, "/"),
		options:    options,
		transfer:   cfg.transfer,
	}, nil
}

// HTTPClient is the first argument of every generated New…ServiceClient.
func (c *Client) HTTPClient() connect.HTTPClient { return c.httpClient }

// BaseURL is the second argument of every generated New…ServiceClient.
func (c *Client) BaseURL() string { return c.baseURL }

// ClientOptions are the remaining arguments of every generated
// New…ServiceClient. The slice is a copy.
func (c *Client) ClientOptions() []connect.ClientOption {
	return append([]connect.ClientOption(nil), c.options...)
}
