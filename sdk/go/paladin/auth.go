package paladin

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"connectrpc.com/connect"

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
	auth    paladiniamv1connect.AuthServiceClient
	now     func() time.Time
	login   *iamv1.LoginRequest // nil when the session started from a refresh token
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

// NewSession signs in at the IAM plane (iamURL) as subject. The password is
// kept so the session can sign in again once its refresh token expires.
func NewSession(ctx context.Context, iamURL, subject, password string, opts ...SessionOption) (*Session, error) {
	s, err := newSession(iamURL, opts...)
	if err != nil {
		return nil, err
	}
	s.login = &iamv1.LoginRequest{Subject: subject, Password: password, RequestedAudience: AudienceIAM}
	s.mu.Lock()
	defer s.mu.Unlock()
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
	c, err := New(iamURL)
	if err != nil {
		return nil, err
	}
	s := &Session{
		auth:   paladiniamv1connect.NewAuthServiceClient(c.HTTPClient(), c.BaseURL(), c.ClientOptions()...),
		now:    time.Now,
		cached: map[string]cachedToken{},
	}
	for _, opt := range opts {
		opt(s)
	}
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
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.cached[audience]; ok && s.now().Add(TokenRefreshMargin).Before(c.expires) {
		return c.token, nil
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
	return s.cached[audience].token, nil
}

// Invalidate drops the cached token for audience; the next Token mints one.
func (s *Session) Invalidate(audience string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.cached, audience)
}

// signIn logs in and caches the IAM token. Called with mu held.
func (s *Session) signIn(ctx context.Context) error {
	resp, err := s.auth.Login(ctx, connect.NewRequest(s.login))
	if err != nil {
		return fmt.Errorf("paladin: sign in: %w", err)
	}
	return s.keepPair(resp.Msg.GetTokens())
}

// mint obtains an access token for audience. Called with mu held.
func (s *Session) mint(ctx context.Context, audience string) error {
	if audience == AudienceIAM {
		resp, err := s.auth.RefreshToken(ctx, connect.NewRequest(&iamv1.RefreshTokenRequest{
			RefreshToken: s.refresh, RequestedAudience: AudienceIAM,
		}))
		if err != nil {
			return fmt.Errorf("paladin: refresh: %w", err)
		}
		return s.keepPair(resp.Msg.GetTokens())
	}
	resp, err := s.auth.ExchangeAudience(ctx, connect.NewRequest(&iamv1.ExchangeAudienceRequest{
		RefreshToken: s.refresh, TargetAudience: audience,
	}))
	if err != nil {
		return fmt.Errorf("paladin: exchange for %s: %w", audience, err)
	}
	s.keep(audience, resp.Msg.GetAccessToken(), resp.Msg.GetAccessExpiresInSeconds())
	return nil
}

// keepPair stores a rotated refresh token and its IAM access token.
func (s *Session) keepPair(p *iamv1.TokenPair) error {
	if p.GetAccessToken() == "" || p.GetRefreshToken() == "" {
		return fmt.Errorf("paladin: sign in: %w", ErrNoToken)
	}
	s.refresh = p.GetRefreshToken()
	s.keep(AudienceIAM, p.GetAccessToken(), p.GetAccessExpiresInSeconds())
	return nil
}

func (s *Session) keep(audience, token string, expiresInSeconds int32) {
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

func (a *tokenAuth) set(ctx context.Context, h http.Header) error {
	token, err := a.source.Token(ctx, a.audience)
	if err != nil {
		return connect.NewError(connect.CodeUnauthenticated, err)
	}
	h.Set(HeaderAuthorization, bearerScheme+" "+token)
	return nil
}

func (a *tokenAuth) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		if err := a.set(ctx, req.Header()); err != nil {
			return nil, err
		}
		resp, err := next(ctx, req)
		inv, ok := a.source.(tokenInvalidator)
		if connect.CodeOf(err) != connect.CodeUnauthenticated || !ok {
			return resp, err
		}
		inv.Invalidate(a.audience)
		if err := a.set(ctx, req.Header()); err != nil {
			return nil, err
		}
		return next(ctx, req)
	}
}

func (a *tokenAuth) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return func(ctx context.Context, spec connect.Spec) connect.StreamingClientConn {
		conn := next(ctx, spec)
		// A stream has nowhere to return the error before it is used; a
		// missing token surfaces as the server's Unauthenticated.
		_ = a.set(ctx, conn.RequestHeader())
		return conn
	}
}

func (a *tokenAuth) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return next
}
