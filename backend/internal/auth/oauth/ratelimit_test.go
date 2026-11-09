package oauth

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/ratecount"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// badCodeForm is a token request that fails on its code once admitted, so an
// admitted call answers 400 and a throttled one 429.
func badCodeForm() url.Values {
	return url.Values{
		"grant_type": {"authorization_code"}, "client_id": {"desktop-agent"},
		"code": {"nope"}, "redirect_uri": {"desktop-agent://cb"},
		"code_verifier": {strings.Repeat("a", 50)},
	}
}

// replica is one AS process counting on rate.
func replica(t *testing.T, rate RateLimiter, perMinute int) *Handler {
	t.Helper()
	store := newMemStore()
	store.clients[publicClient().ClientID] = publicClient()
	h := testHandlerCfg(t, store, &memRefresh{}, sampleUser(), config.OAuthAS{
		Enabled: true, TokenRateLimitPerMinute: perMinute,
		AllowedRedirectSchemes: []string{"https", "desktop-agent"},
	})
	h.rate = rate
	return h
}

// Two replicas on one store hold a client to one ceiling, where each used to
// grant the full rate on its own.
func TestTokenReplicasShareTheClientsCeiling(t *testing.T) {
	shared := ratecount.NewKeyLimiter(ratecount.NewLocal())
	a, b := replica(t, shared, 1), replica(t, shared, 1)
	if rec := postForm(a, "/oauth/token", badCodeForm()); rec.Code == http.StatusTooManyRequests {
		t.Fatal("the first call was throttled")
	}
	if rec := postForm(b, "/oauth/token", badCodeForm()); rec.Code != http.StatusTooManyRequests {
		t.Fatalf("the other replica answered %d past the shared ceiling, want 429", rec.Code)
	}
}

// failingRate decides nothing.
type failingRate struct{}

func (failingRate) Allow(context.Context, string, int) (bool, time.Duration, error) {
	return false, 0, errors.New("store down")
}

// A limit that cannot be decided refuses the call before any work: a token
// request carries a client secret or a code.
func TestTokenRefusesWhatTheLimitCannotDecide(t *testing.T) {
	h := replica(t, failingRate{}, 0)
	rec := postForm(h, "/oauth/token", badCodeForm())
	if rec.Code != http.StatusServiceUnavailable || !onlyError(rec.Body.String(), errCodeTemporarilyUnavailable) {
		t.Fatalf("status %d body %s, want 503 and %s alone", rec.Code, rec.Body, errCodeTemporarilyUnavailable)
	}
}

// onlyError reports whether body is one OAuth error, code: a throttled call
// stops there, and the grant is never run to write a second.
func onlyError(body, code string) bool {
	return strings.Count(body, `"error"`) == 1 && strings.Contains(body, code)
}

// refusing refuses every call, telling it to wait wait.
type refusing struct{ wait time.Duration }

func (r refusing) Allow(context.Context, string, int) (bool, time.Duration, error) {
	return false, r.wait, nil
}

// A refused call is told to wait at least a second, rounded up, and gets no
// further than the refusal.
func TestTokenRefusalSaysWhenToRetry(t *testing.T) {
	for wait, want := range map[time.Duration]string{0: "1", 1500 * time.Millisecond: "2"} {
		rec := postForm(replica(t, refusing{wait}, 0), "/oauth/token", badCodeForm())
		if rec.Code != http.StatusTooManyRequests || !onlyError(rec.Body.String(), errCodeSlowDown) {
			t.Fatalf("wait %v: status %d body %s, want 429 and %s alone", wait, rec.Code, rec.Body, errCodeSlowDown)
		}
		if got := rec.Header().Get(paladin.HeaderRetryAfter); got != want {
			t.Errorf("wait %v: %s = %q, want %q", wait, paladin.HeaderRetryAfter, got, want)
		}
	}
}

// No ceiling configured is the default, never none.
func TestPerMinuteDefaults(t *testing.T) {
	for configured, want := range map[int]int{0: defaultTokenRatePerMinute, -1: defaultTokenRatePerMinute, 5: 5} {
		if got := perMinute(configured); got != want {
			t.Errorf("perMinute(%d) = %d, want %d", configured, got, want)
		}
	}
}
