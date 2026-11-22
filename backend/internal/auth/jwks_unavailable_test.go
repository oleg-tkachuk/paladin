package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect/v2"
)

// The kids the tests' tokens name: one the set never serves.
const (
	servedKid   = "k-served"
	unservedKid = "k-rotated-in"
)

// What a refresh token is told of a key set that would not load.
const refreshKeysUnavailable = "the keys that verify a refresh token could not be loaded; retry the call"

// tokenWithKid is a compact JWS naming kid, which parses and verifies by no
// key: the verifier looks its kid up before it checks a signature.
func tokenWithKid(t *testing.T, kid string) string {
	t.Helper()
	header, err := json.Marshal(map[string]string{"alg": "RS256", "kid": kid})
	if err != nil {
		t.Fatal(err)
	}
	enc := base64.RawURLEncoding
	return enc.EncodeToString(header) + "." + enc.EncodeToString([]byte(`{}`)) + "." + enc.EncodeToString([]byte("sig"))
}

// jwksServing is a verifier of a set served by handler.
func jwksServing(t *testing.T, handler http.HandlerFunc) *JWKSVerifier {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	v := NewJWKSVerifier(srv.URL)
	v.HTTPClient = srv.Client()
	v.MinRefreshInterval = time.Nanosecond
	return v
}

// A token whose key the verifier must fetch, from an identity provider that
// does not answer, is the server failing to decide: Unavailable with a fixed
// sentence, the fetch error kept as the cause for the failure log. The token
// may be good, so the caller is told to retry, not to replace it.
func TestAKeySetThatWillNotLoadIsUnavailable(t *testing.T) {
	v := jwksServing(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	})
	_, err := v.Verify(t.Context(), tokenWithKid(t, unservedKid))
	if !errors.Is(err, ErrKeySetUnavailable) {
		t.Fatalf("Verify = %v, want %v", err, ErrKeySetUnavailable)
	}
	for _, tc := range []struct {
		name string
		got  *connect.Error
		want string
	}{
		{"bearer", bearerRefusal(err), bearerKeysUnavailable},
		{"refresh", RefreshRefusal(err), refreshKeysUnavailable},
	} {
		if tc.got.Code() != connect.CodeUnavailable || tc.got.Message() != tc.want {
			t.Errorf("%s: %v %q, want %v %q", tc.name, tc.got.Code(), tc.got.Message(), connect.CodeUnavailable, tc.want)
		}
		if !errors.Is(tc.got, err) {
			t.Errorf("%s: the fetch error is not the cause", tc.name)
		}
	}
}

// A kid the set does not hold after a refresh that succeeded is the token's
// fault, and stays Unauthenticated.
func TestAnUnknownKidAfterAGoodRefreshIsUnauthenticated(t *testing.T) {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	keys := map[string]any{"keys": []map[string]any{rsaJWK(priv, servedKid, "sig")}}
	v := jwksServing(t, func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(keys)
	})
	_, err = v.Verify(t.Context(), tokenWithKid(t, unservedKid))
	got := bearerRefusal(err)
	if got.Code() != connect.CodeUnauthenticated || got.Message() != ErrBearerUnknownKey.Error() {
		t.Errorf("%v %q, want %v %q", got.Code(), got.Message(), connect.CodeUnauthenticated, ErrBearerUnknownKey)
	}
}
