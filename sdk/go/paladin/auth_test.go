package paladin_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"

	iamv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1/paladiniamv1connect"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// tokenLifetime is how long the fake IAM's access tokens live.
const tokenLifetime = 15 * time.Minute

// parallelCallers is how many goroutines ask for the same token at once.
const parallelCallers = 16

// fakeIAM issues numbered tokens, counts each kind of call, and can be told
// to refuse refresh tokens (as if they had expired) or a set number of
// authenticated calls.
type fakeIAM struct {
	paladiniamv1connect.UnimplementedAuthServiceHandler
	paladiniamv1connect.UnimplementedHealthServiceHandler

	mu              sync.Mutex
	logins          int
	refreshes       int
	exchanges       int
	serial          int
	validRefresh    string
	refuseRefresh   bool
	refuseNextCalls int
	seenTokens      []string
}

func (f *fakeIAM) next(prefix string) string {
	f.serial++
	return fmt.Sprintf("%s-%d", prefix, f.serial)
}

func (f *fakeIAM) pair() *iamv1.TokenPair {
	f.validRefresh = f.next("refresh")
	return &iamv1.TokenPair{
		AccessToken:            f.next("iam"),
		RefreshToken:           f.validRefresh,
		AccessExpiresInSeconds: int32(tokenLifetime / time.Second),
	}
}

func (f *fakeIAM) checkRefresh(token string) error {
	if f.refuseRefresh || token != f.validRefresh {
		return connect.NewError(connect.CodeUnauthenticated, errors.New("refresh token expired"))
	}
	return nil
}

func (f *fakeIAM) Login(context.Context, *connect.Request[iamv1.LoginRequest]) (*connect.Response[iamv1.LoginResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.logins++
	f.refuseRefresh = false
	return connect.NewResponse(&iamv1.LoginResponse{Tokens: f.pair()}), nil
}

func (f *fakeIAM) RefreshToken(_ context.Context, req *connect.Request[iamv1.RefreshTokenRequest]) (*connect.Response[iamv1.RefreshTokenResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.refreshes++
	if err := f.checkRefresh(req.Msg.GetRefreshToken()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&iamv1.RefreshTokenResponse{Tokens: f.pair()}), nil
}

func (f *fakeIAM) ExchangeAudience(_ context.Context, req *connect.Request[iamv1.ExchangeAudienceRequest]) (*connect.Response[iamv1.ExchangeAudienceResponse], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.exchanges++
	if err := f.checkRefresh(req.Msg.GetRefreshToken()); err != nil {
		return nil, err
	}
	return connect.NewResponse(&iamv1.ExchangeAudienceResponse{
		AccessToken:            f.next(req.Msg.GetTargetAudience()),
		AccessExpiresInSeconds: int32(tokenLifetime / time.Second),
	}), nil
}

// GetVersion stands in for any authenticated call: it records the token and
// refuses it while refuseNextCalls lasts.
func (f *fakeIAM) GetVersion(_ context.Context, req *connect.Request[iamv1.GetVersionRequest]) (*connect.Response[iamv1.VersionInfo], error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.seenTokens = append(f.seenTokens, req.Header().Get(paladin.HeaderAuthorization))
	if f.refuseNextCalls > 0 {
		f.refuseNextCalls--
		return nil, connect.NewError(connect.CodeUnauthenticated, errors.New("token revoked"))
	}
	return connect.NewResponse(&iamv1.VersionInfo{}), nil
}

func (f *fakeIAM) counts() (logins, refreshes, exchanges int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.logins, f.refreshes, f.exchanges
}

func serveIAM(t *testing.T, f *fakeIAM) string {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle(paladiniamv1connect.NewAuthServiceHandler(f))
	mux.Handle(paladiniamv1connect.NewHealthServiceHandler(f))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv.URL
}

// clock is a settable time source.
type clock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *clock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func newTestSession(t *testing.T, f *fakeIAM) (*paladin.Session, *clock, string) {
	t.Helper()
	url := serveIAM(t, f)
	clk := &clock{now: time.Unix(0, 0)}
	s, err := paladin.NewSession(context.Background(), url, "admin", "secret", paladin.WithSessionClock(clk.Now))
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	return s, clk, url
}

func mustToken(t *testing.T, s *paladin.Session, audience string) string {
	t.Helper()
	tok, err := s.Token(context.Background(), audience)
	if err != nil {
		t.Fatalf("Token(%s): %v", audience, err)
	}
	return tok
}

func TestSessionCachesEachAudience(t *testing.T) {
	f := &fakeIAM{}
	s, _, _ := newTestSession(t, f)

	iam := mustToken(t, s, paladin.AudienceIAM)
	data := mustToken(t, s, paladin.AudienceData)
	if again := mustToken(t, s, paladin.AudienceData); again != data {
		t.Errorf("a cached data token was replaced: %q then %q", data, again)
	}
	if again := mustToken(t, s, paladin.AudienceIAM); again != iam {
		t.Errorf("the sign-in token was replaced: %q then %q", iam, again)
	}
	if logins, refreshes, exchanges := f.counts(); logins != 1 || refreshes != 0 || exchanges != 1 {
		t.Errorf("logins/refreshes/exchanges = %d/%d/%d, want 1/0/1", logins, refreshes, exchanges)
	}
}

func TestSessionRenewsBeforeExpiry(t *testing.T) {
	f := &fakeIAM{}
	s, clk, _ := newTestSession(t, f)
	iam := mustToken(t, s, paladin.AudienceIAM)
	data := mustToken(t, s, paladin.AudienceData)
	before := s.RefreshToken()

	clk.advance(tokenLifetime - paladin.TokenRefreshMargin + time.Second)

	if got := mustToken(t, s, paladin.AudienceIAM); got == iam {
		t.Error("an IAM token inside the refresh margin was reused")
	}
	if s.RefreshToken() == before {
		t.Error("refreshing did not keep the rotated refresh token")
	}
	if got := mustToken(t, s, paladin.AudienceData); got == data {
		t.Error("a data token inside the refresh margin was reused")
	}
	if _, refreshes, exchanges := f.counts(); refreshes != 1 || exchanges != 2 {
		t.Errorf("refreshes/exchanges = %d/%d, want 1/2", refreshes, exchanges)
	}
}

func TestSessionSignsInAgainWhenTheRefreshTokenExpires(t *testing.T) {
	f := &fakeIAM{}
	s, _, _ := newTestSession(t, f)
	f.mu.Lock()
	f.refuseRefresh = true
	f.mu.Unlock()

	if _, err := s.Token(context.Background(), paladin.AudienceAdmin); err != nil {
		t.Fatalf("Token after the refresh token expired: %v", err)
	}
	if logins, _, _ := f.counts(); logins != 2 {
		t.Errorf("logins = %d, want 2: the session should sign in again", logins)
	}
}

func TestSessionFromRefreshTokenCannotSignInAgain(t *testing.T) {
	f := &fakeIAM{validRefresh: "known"}
	url := serveIAM(t, f)
	s, err := paladin.SessionFromRefreshToken(url, "unknown")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Token(context.Background(), paladin.AudienceData); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("err = %v, want Unauthenticated", err)
	}
	if logins, _, _ := f.counts(); logins != 0 {
		t.Errorf("a session without a password signed in %d times", logins)
	}
	if _, err := paladin.SessionFromRefreshToken(url, ""); !errors.Is(err, paladin.ErrNoToken) {
		t.Errorf("an empty refresh token: err = %v, want ErrNoToken", err)
	}
}

func TestSessionMintsOnceForConcurrentCallers(t *testing.T) {
	f := &fakeIAM{}
	s, _, _ := newTestSession(t, f)
	var wg sync.WaitGroup
	for range parallelCallers {
		wg.Go(func() { _, _ = s.Token(context.Background(), paladin.AudienceData) })
	}
	wg.Wait()
	if _, _, exchanges := f.counts(); exchanges != 1 {
		t.Errorf("exchanges = %d for %d concurrent callers, want 1", exchanges, parallelCallers)
	}
}

func healthWith(t *testing.T, url string, opts ...paladin.Option) paladiniamv1connect.HealthServiceClient {
	t.Helper()
	c, err := paladin.New(url, opts...)
	if err != nil {
		t.Fatal(err)
	}
	return paladiniamv1connect.NewHealthServiceClient(c.HTTPClient(), c.BaseURL(), c.ClientOptions()...)
}

func TestTokenSourceAuthenticatesAndRetriesARefusedTokenOnce(t *testing.T) {
	f := &fakeIAM{}
	s, _, url := newTestSession(t, f)
	health := healthWith(t, url, paladin.WithTokenSource(s, paladin.AudienceIAM))

	f.mu.Lock()
	f.refuseNextCalls = 1
	f.mu.Unlock()
	if _, err := health.GetVersion(context.Background(), connect.NewRequest(&iamv1.GetVersionRequest{})); err != nil {
		t.Fatalf("a call whose token was refused once: %v", err)
	}
	f.mu.Lock()
	seen := append([]string(nil), f.seenTokens...)
	f.refuseNextCalls = 2
	f.mu.Unlock()
	if len(seen) != 2 || seen[0] == seen[1] || seen[0] == "" {
		t.Fatalf("tokens sent = %q, want two different bearer tokens", seen)
	}

	if _, err := health.GetVersion(context.Background(), connect.NewRequest(&iamv1.GetVersionRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("err = %v, want Unauthenticated after the retry was refused too", err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.seenTokens) != len(seen)+2 {
		t.Errorf("calls = %d, want %d: a refused token is retried once, not more", len(f.seenTokens), len(seen)+2)
	}
}

func TestStaticToken(t *testing.T) {
	f := &fakeIAM{}
	url := serveIAM(t, f)
	health := healthWith(t, url, paladin.WithTokenSource(paladin.StaticToken("paladin_pat_abc"), paladin.AudienceIAM))
	if _, err := health.GetVersion(context.Background(), connect.NewRequest(&iamv1.GetVersionRequest{})); err != nil {
		t.Fatal(err)
	}
	if got := f.seenTokens[0]; got != "Bearer paladin_pat_abc" {
		t.Errorf("sent %q", got)
	}

	empty := healthWith(t, url, paladin.WithTokenSource(paladin.StaticToken(""), paladin.AudienceIAM))
	if _, err := empty.GetVersion(context.Background(), connect.NewRequest(&iamv1.GetVersionRequest{})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Errorf("err = %v, want Unauthenticated without a token", err)
	}
	if len(f.seenTokens) != 1 {
		t.Errorf("a call without a token reached the server")
	}
}

// blockingIAM holds every ExchangeAudience until released, and can be told to
// fail them with a code.
type blockingIAM struct {
	fakeIAM
	hold    chan struct{}
	entered chan struct{}
	failing connect.Code
}

func (b *blockingIAM) ExchangeAudience(ctx context.Context, req *connect.Request[iamv1.ExchangeAudienceRequest]) (*connect.Response[iamv1.ExchangeAudienceResponse], error) {
	if b.failing != 0 {
		return nil, connect.NewError(b.failing, errors.New("iam is down"))
	}
	if b.entered != nil {
		b.entered <- struct{}{}
	}
	if b.hold != nil {
		<-b.hold
	}
	return b.fakeIAM.ExchangeAudience(ctx, req)
}

// Every Token held the session's lock across its call to IAM, so one slow
// exchange stalled callers whose token was already cached.
func TestSessionServesACachedTokenWhileAnotherIsMinted(t *testing.T) {
	b := &blockingIAM{hold: make(chan struct{}), entered: make(chan struct{}, 1)}
	mux := http.NewServeMux()
	mux.Handle(paladiniamv1connect.NewAuthServiceHandler(b))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	s, err := paladin.NewSession(context.Background(), srv.URL, "admin", "secret")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	cached := mustToken(t, s, paladin.AudienceIAM)

	minted := make(chan error, 1)
	go func() {
		_, err := s.Token(context.Background(), paladin.AudienceData)
		minted <- err
	}()
	<-b.entered // the data exchange is now in flight, and held

	done := make(chan string, 1)
	go func() { done <- mustToken(t, s, paladin.AudienceIAM) }()
	select {
	case got := <-done:
		if got != cached {
			t.Errorf("IAM token = %q, want the cached %q", got, cached)
		}
	case <-time.After(time.Second):
		// Release the exchange first, or the server cannot close.
		close(b.hold)
		t.Fatal("a cached token waited for another audience's exchange")
	}
	close(b.hold)
	if err := <-minted; err != nil {
		t.Fatalf("data token: %v", err)
	}
}

// IAM being down was reported as Unauthenticated, which reads as bad
// credentials and is not worth retrying. It keeps its own code now.
func TestSessionReportsAnIAMOutageAsUnavailable(t *testing.T) {
	b := &blockingIAM{failing: connect.CodeUnavailable}
	mux := http.NewServeMux()
	mux.Handle(paladiniamv1connect.NewAuthServiceHandler(b))
	mux.Handle(paladiniamv1connect.NewHealthServiceHandler(b))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	s, err := paladin.NewSession(context.Background(), srv.URL, "admin", "secret")
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	c, err := paladin.New(srv.URL, paladin.WithTokenSource(s, paladin.AudienceData))
	if err != nil {
		t.Fatal(err)
	}
	health := paladiniamv1connect.NewHealthServiceClient(c.HTTPClient(), c.BaseURL(), c.ClientOptions()...)
	_, err = health.GetVersion(context.Background(), connect.NewRequest(&iamv1.GetVersionRequest{}))
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("code = %v, want Unavailable for an IAM outage", connect.CodeOf(err))
	}
	if errors.Is(err, paladin.ErrUnauthenticated) {
		t.Error("an IAM outage matched ErrUnauthenticated")
	}
}

// The session reached IAM over a client with New's defaults, whatever the
// caller's planes used — no CA, no client certificate, no retries.
func TestSessionClientOptionsReachIAM(t *testing.T) {
	const header, value = "X-Session-Probe", "set-by-the-caller"
	var seen string
	f := &fakeIAM{}
	mux := http.NewServeMux()
	path, h := paladiniamv1connect.NewAuthServiceHandler(f)
	mux.Handle(path, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.Header.Get(header)
		h.ServeHTTP(w, r)
	}))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	if _, err := paladin.NewSession(context.Background(), srv.URL, "admin", "secret",
		paladin.WithSessionClientOptions(paladin.WithHeader(header, value))); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if seen != value {
		t.Errorf("%s = %q at IAM, want %q", header, seen, value)
	}
}
