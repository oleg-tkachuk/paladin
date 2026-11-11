package app

import (
	"errors"
	"testing"
	"time"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/biscuit"
)

// A capability past its expiry is refused at once under a zero
// verifier_leeway, and accepted within a configured one: limes takes a zero
// Leeway as its 30s default unless told there is none.
func TestVerifierLeewayZeroHasNone(t *testing.T) {
	const pastExpiry = time.Second
	for _, tc := range []struct {
		name       string
		configured time.Duration
		accepted   bool
	}{
		{"zero is none", 0, false},
		{"within the configured leeway", limes.DefaultLeeway, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			issuers, _, store, _ := delegationFixture(t)
			token, err := issueWith(t, issuers.issuer)
			if err != nil {
				t.Fatal(err)
			}
			leeway, none := verifierLeeway(tc.configured)
			at := time.Now().Add(testCapTTL + pastExpiry)
			verifier, err := limes.NewVerifier(limes.VerifierConfig{
				Keys: issuers.keys, Revocations: store, TrustedIssuers: issuers.trusted, KeyIssuers: issuers.keyIssuers,
				Biscuit: &limes.BiscuitConfig{Form: biscuit.Form(), Revocations: store},
				Leeway:  leeway, NoLeeway: none,
				Now: func() time.Time { return at },
			})
			if err != nil {
				t.Fatal(err)
			}
			_, err = verifier.Verify(t.Context(), limes.VerifyRequest{Token: token, Audience: testCapAudience})
			if got := err == nil; got != tc.accepted {
				t.Fatalf("accepted = %v (%v), want %v", got, err, tc.accepted)
			}
			if !tc.accepted && !errors.Is(err, limes.ErrExpired) {
				t.Errorf("err = %v, want limes.ErrExpired", err)
			}
		})
	}
}
