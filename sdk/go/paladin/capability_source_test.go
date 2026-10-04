package paladin_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"connectrpc.com/connect"

	iamv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1/paladiniamv1connect"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// headerRecorder answers every call with an error and keeps the headers of
// the last request: these tests are about what the client sends.
func headerRecorder(t *testing.T) (string, func() http.Header) {
	t.Helper()
	var (
		mu   sync.Mutex
		last http.Header
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		last = r.Header.Clone()
		mu.Unlock()
		http.Error(w, "recorded", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	return srv.URL, func() http.Header {
		mu.Lock()
		defer mu.Unlock()
		return last
	}
}

type callerKey struct{}

func callerCapability(ctx context.Context) string {
	token, _ := ctx.Value(callerKey{}).(string)
	return token
}

func callHealth(t *testing.T, ctx context.Context, url string, opts ...paladin.Option) {
	t.Helper()
	c, err := paladin.New(url, opts...)
	if err != nil {
		t.Fatal(err)
	}
	health := paladiniamv1connect.NewHealthServiceClient(c.HTTPClient(), c.BaseURL(), c.ClientOptions()...)
	_, _ = health.GetVersion(ctx, connect.NewRequest(&iamv1.GetVersionRequest{}))
}

// Each call carries its own caller's capability; a call whose context has
// none keeps the client's static one.
func TestWithCapabilitySourceSendsEachCallersCapability(t *testing.T) {
	url, last := headerRecorder(t)
	opts := []paladin.Option{paladin.WithCapability("static-cap"), paladin.WithCapabilitySource(callerCapability)}

	callHealth(t, context.WithValue(context.Background(), callerKey{}, "caller-cap"), url, opts...)
	if got := last().Get(paladin.HeaderCapability); got != "caller-cap" {
		t.Errorf("with a caller's capability: sent %q", got)
	}
	callHealth(t, context.Background(), url, opts...)
	if got := last().Get(paladin.HeaderCapability); got != "static-cap" {
		t.Errorf("without one: sent %q, want the static capability", got)
	}
}

// The DPoP proof is signed over the capability the call carries, so it must
// see the per-call one.
func TestWithCapabilitySourceIsWhatDPoPSigns(t *testing.T) {
	url, last := headerRecorder(t)
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	callHealth(t, context.WithValue(context.Background(), callerKey{}, "caller-cap"), url,
		paladin.WithCapabilitySource(callerCapability), paladin.WithDPoP(key))

	parts := strings.Split(last().Get(paladin.HeaderDPoP), ".")
	if len(parts) != 3 {
		t.Fatalf("no DPoP proof sent: %q", last().Get(paladin.HeaderDPoP))
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatal(err)
	}
	var claims struct {
		ATH string `json:"ath"`
	}
	if err := json.Unmarshal(raw, &claims); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("caller-cap"))
	if want := base64.RawURLEncoding.EncodeToString(sum[:]); claims.ATH != want {
		t.Errorf("ath = %q, want the hash of the caller's capability", claims.ATH)
	}
}

// A token source with nothing to send and no error sends no Authorization,
// so a capability alone authenticates the call.
func TestAnEmptyTokenSendsNoAuthorization(t *testing.T) {
	url, last := headerRecorder(t)
	none := tokenFunc(func(context.Context, string) (string, error) { return "", nil })
	callHealth(t, context.Background(), url,
		paladin.WithTokenSource(none, paladin.AudienceIAM), paladin.WithCapability("cap"))
	if got, ok := last()[paladin.HeaderAuthorization]; ok {
		t.Errorf("Authorization sent: %q", got)
	}
	if got := last().Get(paladin.HeaderCapability); got != "cap" {
		t.Errorf("capability = %q", got)
	}
}

type tokenFunc func(context.Context, string) (string, error)

func (f tokenFunc) Token(ctx context.Context, audience string) (string, error) {
	return f(ctx, audience)
}
