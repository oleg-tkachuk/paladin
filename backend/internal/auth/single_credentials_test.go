package auth

import (
	"errors"
	"testing"

	"connectrpc.com/connect/v2"
	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/dpop"

	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// A request presents one Authorization field and one DPoP proof at most:
// RFC 9449 §7.2 and RFC 6750 §2 refuse a token presented twice, §4.3 a second
// proof, so neither is picked. One of each, or none, reaches the next
// interceptor untouched.
func TestSingleCredentials(t *testing.T) {
	t.Parallel()
	const (
		tokenA = "Bearer a"
		tokenB = "Bearer b"
		proofA = "proof-a"
		proofB = "proof-b"
	)
	for name, tc := range map[string]struct {
		pairs   []string
		refused error
	}{
		"none":                 {nil, nil},
		"one of each":          {[]string{paladin.HeaderAuthorization, tokenA, dpop.Header, proofA}, nil},
		"two Authorization":    {[]string{paladin.HeaderAuthorization, tokenA, paladin.HeaderAuthorization, tokenB}, limes.ErrMalformedAuthorization},
		"two proofs":           {[]string{paladin.HeaderAuthorization, tokenA, dpop.Header, proofA, dpop.Header, proofB}, dpop.ErrInvalid},
		"two proofs, no token": {[]string{dpop.Header, proofA, dpop.Header, proofB}, dpop.ErrInvalid},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			call := callProbe(t.Context(), []connect.ServerInterceptor{SingleCredentials()}, tc.pairs...)
			if tc.refused == nil {
				if !call.reached() || call.err != nil {
					t.Fatalf("refused: %v", call.err)
				}
				return
			}
			if call.reached() {
				t.Fatal("reached the handler")
			}
			if connect.CodeOf(call.err) != connect.CodeInvalidArgument || !errors.Is(call.err, tc.refused) {
				t.Errorf("err = %v (%s), want InvalidArgument wrapping %v", call.err, connect.CodeOf(call.err), tc.refused)
			}
		})
	}
}

// A stream is held to the same: its interceptors read the header too.
func TestSingleCredentialsStream(t *testing.T) {
	t.Parallel()
	header := &connect.Header{}
	header.Add(paladin.HeaderAuthorization, "Bearer a")
	header.Add(paladin.HeaderAuthorization, "Bearer b")
	seen, err := callStream(t.Context(), []connect.ServerInterceptor{SingleCredentials()}, header)
	if seen != nil || !errors.Is(err, limes.ErrMalformedAuthorization) {
		t.Errorf("reached = %v, err = %v; want refused with limes.ErrMalformedAuthorization", seen != nil, err)
	}
}
