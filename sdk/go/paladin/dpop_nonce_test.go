package paladin_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"

	"github.com/oleg-tkachuk/limes/dpop"

	iamv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1/paladiniamv1connect"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// serverNonce is the nonce the fake hands out with each response.
const serverNonce = "server-nonce"

// proofRefuser refuses the first call it sees with a DPoP challenge naming
// challengeError and a nonce to retry with, serves every later one, and
// keeps the proof each call carried.
type proofRefuser struct {
	paladiniamv1connect.UnimplementedHealthServiceHandler
	challengeError string

	mu     sync.Mutex
	proofs []string
}

func (f *proofRefuser) GetVersion(ctx context.Context, _ *iamv1.GetVersionRequest) (*iamv1.VersionInfo, error) {
	info, _ := connect.CallInfoForServerContext(ctx)
	f.mu.Lock()
	defer f.mu.Unlock()
	f.proofs = append(f.proofs, info.RequestHeader().Get(paladin.HeaderDPoP))
	info.ResponseHeader().Set(paladin.HeaderDPoPNonce, serverNonce)
	if len(f.proofs) == 1 {
		info.ResponseHeader().Set(paladin.HeaderWWWAuthenticate, `DPoP error="`+f.challengeError+`"`)
		return nil, connect.NewError(connect.CodeUnauthenticated, "proof refused")
	}
	return &iamv1.VersionInfo{}, nil
}

func (f *proofRefuser) seen() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.proofs...)
}

// A unary call refused for want of the server's nonce is sent once more,
// with a fresh proof; one whose proof was refused for any other fault is
// not.
func TestDPoPResendsACallOnlyForANonceChallenge(t *testing.T) {
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct {
		challengeError string
		attempts       int
		refused        bool
	}{
		"a nonce asked for": {dpop.ErrorCodeUseNonce, 2, false},
		"a proof refused":   {dpop.ErrorCodeInvalidProof, 1, true},
	} {
		t.Run(name, func(t *testing.T) {
			f := &proofRefuser{challengeError: tc.challengeError}
			mux := http.NewServeMux()
			server := connect.NewServer()
			paladiniamv1connect.RegisterHealthServiceHandler(server, f)
			connecthttp.Mount(mux, server)
			srv := httptest.NewServer(mux)
			t.Cleanup(srv.Close)

			health := healthWith(t, srv.URL, paladin.WithCapability("capability"), paladin.WithDPoP(key))
			_, err := health.GetVersion(context.Background(), &iamv1.GetVersionRequest{})
			if refused := connect.CodeOf(err) == connect.CodeUnauthenticated; refused != tc.refused || !refused && err != nil {
				t.Fatalf("err = %v, want refused %v", err, tc.refused)
			}
			proofs := f.seen()
			if len(proofs) != tc.attempts {
				t.Fatalf("attempts = %d, want %d", len(proofs), tc.attempts)
			}
			if tc.attempts > 1 && proofs[0] == proofs[1] {
				t.Errorf("the resent call repeated its proof")
			}
		})
	}
}
