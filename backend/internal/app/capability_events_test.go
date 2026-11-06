package app

import (
	"context"
	"testing"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/limes"

	"github.com/oleg-tkachuk/paladin/backend/internal/logger"
	"github.com/oleg-tkachuk/paladin/backend/internal/metrics"
)

// capabilityCalls records what an event adapter reports.
type capabilityCalls []string

func (c *capabilityCalls) record(_ context.Context, component, operation, outcome, code string, _ float64) {
	*c = append(*c, component+" "+operation+" "+outcome+" "+code)
}

// An issuer wired with the events counts each token it mints and each
// request it refuses, by operation and code.
func TestIssuerEventsCountEachOutcome(t *testing.T) {
	var calls capabilityCalls
	_, key, store, _ := delegationFixture(t)
	signer, err := limes.NewSigner(limes.SignerConfig{KeyID: key.KeyID, Key: key.Private})
	if err != nil {
		t.Fatal(err)
	}
	issuer, err := limes.NewIssuer(limes.IssuerConfig{
		Signer: signer, Store: store, IssuerName: testIssuerName, DefaultTTL: testCapTTL,
		Events: issuerEvents(calls.record), Logger: logger.Slog(zap.NewNop(), "test"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := issueWith(t, issuer); err != nil {
		t.Fatal(err)
	}
	if _, err := issuer.Issue(t.Context(), limes.IssueRequest{}); err == nil {
		t.Fatal("an empty request was issued")
	}
	if len(calls) != 2 || calls[0] != "issuer issue ok " || calls[1][:len("issuer issue refused ")] != "issuer issue refused " {
		t.Errorf("recorded %q, want one issue ok and one refused with its code", calls)
	}
}

// Each adapter reports under its component, with limes' own operation and
// code, and the outcome its callback stands for.
func TestCapabilityEventsNameTheirComponent(t *testing.T) {
	var calls capabilityCalls
	ctx := context.Background()
	verifierEvents(calls.record).OnRefused(ctx, limes.VerifierErrorEvent{Operation: "verify", Code: limes.CodeExpired})
	verifierEvents(calls.record).OnFailed(ctx, limes.VerifierErrorEvent{Operation: "verify", Code: limes.CodeKeysUnavailable})
	revokerEvents(calls.record).OnRevoked(ctx, limes.RevokedEvent{Operation: "revoke"})
	want := capabilityCalls{
		"verifier verify " + metrics.CapabilityCallRefused + " expired",
		"verifier verify " + metrics.CapabilityCallFailed + " keys_unavailable",
		"revoker revoke " + metrics.CapabilityCallOK + " ",
	}
	if len(calls) != len(want) {
		t.Fatalf("recorded %q, want %q", calls, want)
	}
	for i := range want {
		if calls[i] != want[i] {
			t.Errorf("call %d = %q, want %q", i, calls[i], want[i])
		}
	}
}
