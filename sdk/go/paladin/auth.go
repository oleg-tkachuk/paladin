package paladin

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"connectrpc.com/connect/v2"

	iamv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1/paladiniamv1connect"
)

// Audiences: a token is issued for one plane and refused by the others. The
// server imports these, so the two cannot spell them differently.
const (
	AudienceData  = "paladin-data"
	AudienceAdmin = "paladin-admin"
	AudienceIAM   = "paladin-iam"
)

// TokenRefreshMargin is how long before its expiry a cached access token is
// replaced, so a call does not leave with a token that lapses in flight.
const TokenRefreshMargin = 30 * time.Second

// ErrNoToken is returned by a TokenSource that has nothing to send.
var ErrNoToken = errors.New("paladin: token source has no token")

// TokenSource supplies the bearer token for a call to one audience's plane.
// An empty token with no error sends the call without Authorization, for a
// caller that authenticates another way, such as with a capability.
type TokenSource interface {
	Token(ctx context.Context, audience string) (string, error)
}

// tokenInvalidator is a TokenSource that can drop a token the server refused.
type tokenInvalidator interface {
	Invalidate(audience string)
}

// StaticToken is a TokenSource that sends the same token to every plane: an
// API token, or a JWT obtained elsewhere.
type StaticToken string

// Token returns the token itself.
func (t StaticToken) Token(context.Context, string) (string, error) {
	if t == "" {
		return "", ErrNoToken
	}
	return string(t), nil
}

// WithTokenSource authenticates every call with a token from ts for
// audience, the audience of the plane this client talks to. A call the
// server refuses as Unauthenticated is made once more with a fresh token.
func WithTokenSource(ts TokenSource, audience string) Option {
	return func(cfg *config) { cfg.tokens = &tokenAuth{source: ts, audience: audience} }
}

// Session is a TokenSource for a user who signs in. It keeps one refresh
// token, issued for the IAM plane, and derives each plane's access token from
// it: the IAM token by refreshing, the others by exchange. Access tokens are
// cached until TokenRefreshMargin before they expire. Safe for concurrent use.
type Session struct {
	auth  paladiniamv1connect.AuthServiceClient
	now   func() time.Time
	login *iamv1.LoginRequest // nil when the session started from a refresh token
	// clientOpts configure the IAM client the session signs in with.
	clientOpts []Option
	// mintMu serialises the calls to IAM: a refresh rotates the refresh token,
	// so two at once would race to spend it. mu guards the state and is never
	// held across a call, so a cached token is served while another audience's
	// is being minted.
	mintMu  sync.Mutex
	mu      sync.Mutex
	refresh string
	cached  map[string]cachedToken
}

type cachedToken struct {
	token   string
	expires time.Time
}

// SessionOption configures a Session.
type SessionOption func(*Session)

// WithSessionClock replaces time.Now, for tests.
func WithSessionClock(now func() time.Time) SessionOption {
	return func(s *Session) { s.now = now }
}

// WithSessionClientOptions configures the client the session reaches the IAM
// plane with, as New's options configure any other: WithTLS for an IAM behind
// mTLS, WithHTTPClient, WithRetries, WithHooks. Without it the session signs
// in over a client with New's defaults.
func WithSessionClientOptions(opts ...Option) SessionOption {
	return func(s *Session) { s.clientOpts = append(s.clientOpts, opts...) }
}

// NewSession signs in at the IAM plane (iamURL) as subject. The password is
// kept so the session can sign in again once its refresh token expires.
func NewSession(ctx context.Context, iamURL, subject, password string, opts ...SessionOption) (*Session, error) {
	s, err := newSession(iamURL, opts...)
	if err != nil {
		return nil, err
	}
	s.login = &iamv1.LoginRequest{Subject: subject, Password: password, RequestedAudience: AudienceIAM}
	s.mintMu.Lock()
	defer s.mintMu.Unlock()
	if err := s.signIn(ctx); err != nil {
		return nil, err
	}
	return s, nil
}

// SessionFromRefreshToken resumes a session from a refresh token issued for
// the IAM plane. It cannot sign in again when that token expires.
func SessionFromRefreshToken(iamURL, refreshToken string, opts ...SessionOption) (*Session, error) {
	if refreshToken == "" {
		return nil, ErrNoToken
	}
	s, err := newSession(iamURL, opts...)
	if err != nil {
		return nil, err
	}
	s.refresh = refreshToken
	return s, nil
}

func newSession(iamURL string, opts ...SessionOption) (*Session, error) {
	s := &Session{now: time.Now, cached: map[string]cachedToken{}}
	for _, opt := range opts {
		opt(s)
	}
	c, err := New(iamURL, s.clientOpts...)
	if err != nil {
		return nil, err
	}
	s.auth = paladiniamv1connect.NewAuthServiceClient(c.Connect())
	return s, nil
}

// RefreshToken is the session's current refresh token, to store and resume
// from with SessionFromRefreshToken. Refreshing rotates it.
func (s *Session) RefreshToken() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refresh
}

// Token returns a valid access token for audience.
func (s *Session) Token(ctx context.Context, audience string) (string, error) {
	if token, ok := s.fresh(audience); ok {
		return token, nil
	}
	s.mintMu.Lock()
	defer s.mintMu.Unlock()
	// Another caller may have minted it while this one waited.
	if token, ok := s.fresh(audience); ok {
		return token, nil
	}
	err := s.mint(ctx, audience)
	if connect.CodeOf(err) == connect.CodeUnauthenticated && s.login != nil {
		// The refresh token itself expired or was revoked: sign in again.
		if err = s.signIn(ctx); err == nil {
			err = s.mint(ctx, audience)
		}
	}
	if err != nil {
		return "", err
	}
	token, _ := s.fresh(audience)
	return token, nil
}

// fresh returns the cached token for audience while it has more than
// TokenRefreshMargin left.
func (s *Session) fresh(audience string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.cached[audience]
	if !ok || !s.now().Add(TokenRefreshMargin).Before(c.expires) {
		return "", false
	}
	return c.token, true
}

// refreshToken reads the refresh token under mu.
func (s *Session) refreshToken() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.refresh
}

// Invalidate drops the cached token for audience; the next Token mints one.
func (s *Session) Invalidate(audience string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cached, audience)
}

// signIn logs in and caches the IAM token. Called with mintMu held.
func (s *Session) signIn(ctx context.Context) error {
	resp, err := s.auth.Login(ctx, s.login)
	if err != nil {
		return fmt.Errorf("paladin: sign in: %w", err)
	}
	return s.keepPair(resp.GetTokens())
}

// mint obtains an access token for audience. Called with mintMu held.
func (s *Session) mint(ctx context.Context, audience string) error {
	refresh := s.refreshToken()
	if audience == AudienceIAM {
		resp, err := s.auth.RefreshToken(ctx, &iamv1.RefreshTokenRequest{
			RefreshToken: refresh, RequestedAudience: AudienceIAM,
		})
		if err != nil {
			return fmt.Errorf("paladin: refresh: %w", err)
		}
		return s.keepPair(resp.GetTokens())
	}
	resp, err := s.auth.ExchangeAudience(ctx, &iamv1.ExchangeAudienceRequest{
		RefreshToken: refresh, TargetAudience: audience,
	})
	if err != nil {
		return fmt.Errorf("paladin: exchange for %s: %w", audience, err)
	}
	s.keep(audience, resp.GetAccessToken(), resp.GetAccessExpiresInSeconds())
	return nil
}

// keepPair stores a rotated refresh token and its IAM access token.
func (s *Session) keepPair(p *iamv1.TokenPair) error {
	if p.GetAccessToken() == "" || p.GetRefreshToken() == "" {
		return fmt.Errorf("paladin: sign in: %w", ErrNoToken)
	}
	s.mu.Lock()
	s.refresh = p.GetRefreshToken()
	s.mu.Unlock()
	s.keep(AudienceIAM, p.GetAccessToken(), p.GetAccessExpiresInSeconds())
	return nil
}

func (s *Session) keep(audience, token string, expiresInSeconds int32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cached[audience] = cachedToken{
		token:   token,
		expires: s.now().Add(time.Duration(expiresInSeconds) * time.Second),
	}
}

// tokenAuth puts a TokenSource's token on every call, and makes a call the
// server refused as Unauthenticated once more with a fresh token: the server
// authenticates before it does anything else, so the first attempt changed
// nothing.
type tokenAuth struct {
	source   TokenSource
	audience string
}

func (a *tokenAuth) set(ctx context.Context, h *connect.Header) error {
	token, err := a.source.Token(ctx, a.audience)
	if err != nil {
		return tokenError(err)
	}
	if token == "" {
		h.Delete(HeaderAuthorization)
		return nil
	}
	h.Set(HeaderAuthorization, bearerScheme+" "+token)
	return nil
}

// tokenError is what a call answers when its token could not be had. Only a
// refusal is Unauthenticated: no token at all, or IAM rejecting the
// credential. IAM being down or slow keeps its own code — Unavailable,
// DeadlineExceeded — so the caller can tell an outage from bad credentials and
// retry it; a failure with no Connect code (the connection itself) is
// Unavailable.
func tokenError(err error) error {
	switch code := connect.CodeOf(err); {
	case errors.Is(err, ErrNoToken), code == connect.CodeUnauthenticated:
		return connect.NewError(connect.CodeUnauthenticated, err.Error()).WithCause(err)
	case errors.Is(err, context.Canceled):
		return connect.NewError(connect.CodeCanceled, err.Error()).WithCause(err)
	case errors.Is(err, context.DeadlineExceeded):
		return connect.NewError(connect.CodeDeadlineExceeded, err.Error()).WithCause(err)
	case code == connect.CodeUnknown:
		return connect.NewError(connect.CodeUnavailable, err.Error()).WithCause(err)
	default:
		return connect.NewError(code, err.Error()).WithCause(err)
	}
}

func (a *tokenAuth) interceptor() connect.ClientInterceptor {
	return interceptor(a.unary, a.stream)
}

func (a *tokenAuth) unary(next unaryFunc) unaryFunc {
	return func(ctx context.Context, spec connect.Spec, req, res any) error {
		header := callInfo(ctx).RequestHeader()
		if err := a.set(ctx, header); err != nil {
			return err
		}
		err := next(ctx, spec, req, res)
		inv, ok := a.source.(tokenInvalidator)
		if connect.CodeOf(err) != connect.CodeUnauthenticated || !ok {
			return err
		}
		inv.Invalidate(a.audience)
		if err := a.set(ctx, header); err != nil {
			return err
		}
		clearResponse(callInfo(ctx))
		return next(ctx, spec, req, res)
	}
}

func (a *tokenAuth) stream(next connect.ClientFunc) connect.ClientFunc {
	return func(ctx context.Context, spec connect.Spec) (connect.ClientStream, error) {
		if err := a.set(ctx, callInfo(ctx).RequestHeader()); err != nil {
			return nil, err
		}
		return next(ctx, spec)
	}
}
