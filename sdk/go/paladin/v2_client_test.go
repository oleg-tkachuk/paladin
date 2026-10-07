package paladin_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	iamv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1/paladiniamv1connect"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// A retried call is signed afresh on every attempt — the server refuses a
// DPoP proof it has seen — while every attempt carries the one idempotency
// key the server recognises the repeat by.
func TestEachRetryIsSignedAfreshUnderOneKey(t *testing.T) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	rec := &recorder{failures: 2, failCode: connect.CodeUnavailable}
	_, auth := clients(t, serve(t, rec),
		paladin.WithCapability("cap"), paladin.WithDPoP(key), paladin.WithRetries(3, testRetryDelay))

	if _, err := auth.Login(context.Background(), &iamv1.LoginRequest{}); err != nil {
		t.Fatal(err)
	}
	rec.mu.Lock()
	defer rec.mu.Unlock()
	if len(rec.headers) != 3 {
		t.Fatalf("server saw %d attempts, want 3", len(rec.headers))
	}
	proofs := map[string]bool{}
	firstKey := rec.headers[0].Get(paladin.HeaderIdempotencyKey)
	for i, h := range rec.headers {
		proof := h.Get(paladin.HeaderDPoP)
		if proof == "" || proofs[proof] {
			t.Errorf("attempt %d: DPoP proof %q is missing or repeated", i+1, proof)
		}
		proofs[proof] = true
		if got := h.Get(paladin.HeaderIdempotencyKey); got == "" || got != firstKey {
			t.Errorf("attempt %d: idempotency key %q, want %q", i+1, got, firstKey)
		}
	}
}

// bigVersion answers GetVersion with a version string of size bytes.
type bigVersion struct {
	paladiniamv1connect.UnimplementedHealthServiceHandler
	size int
}

func (b bigVersion) GetVersion(context.Context, *iamv1.GetVersionRequest) (*iamv1.VersionInfo, error) {
	return &iamv1.VersionInfo{Version: strings.Repeat("v", b.size)}, nil
}

// connect-go v2 refuses a response over 4 MiB unless told otherwise; the
// server sends up to MaxResponseBytes, so the client reads up to that.
func TestAResponseUpToMaxResponseBytesIsRead(t *testing.T) {
	const transportDefault = 4 << 20
	server := connect.NewServer()
	paladiniamv1connect.RegisterHealthServiceHandler(server, bigVersion{size: transportDefault + 1})
	mux := http.NewServeMux()
	connecthttp.Mount(mux, server)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	health, _ := clients(t, srv.URL)
	if _, err := health.GetVersion(context.Background(), &iamv1.GetVersionRequest{}); err != nil {
		t.Fatalf("a %d-byte response: %v", transportDefault+1, err)
	}
	if paladin.MaxResponseBytes <= transportDefault {
		t.Fatalf("MaxResponseBytes = %d, not above the transport's default", paladin.MaxResponseBytes)
	}
}
