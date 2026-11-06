package app

import (
	"context"

	"github.com/oleg-tkachuk/limes"

	"github.com/oleg-tkachuk/paladin/backend/internal/metrics"
)

// The components a capability call is counted under
// (metrics.RecordCapabilityCall).
const (
	capabilityComponentIssuer   = "issuer"
	capabilityComponentVerifier = "verifier"
	capabilityComponentRevoker  = "revoker"
)

// capabilityRecorder records one call of a capability component;
// metrics.RecordCapabilityCall is the one the server wires. limes calls the
// events on the request path, after logging the call, so it must only touch
// in-memory instruments.
type capabilityRecorder func(ctx context.Context, component, operation, outcome, code string, seconds float64)

// issuerEvents counts every token the issuer mints, refuses or fails to.
func issuerEvents(record capabilityRecorder) limes.IssuerEvents {
	return limes.IssuerEvents{
		OnIssued: func(ctx context.Context, e limes.IssuedEvent) {
			record(ctx, capabilityComponentIssuer, string(e.Operation), metrics.CapabilityCallOK, "", e.Duration.Seconds())
		},
		OnRefused: func(ctx context.Context, e limes.IssuerErrorEvent) {
			record(ctx, capabilityComponentIssuer, string(e.Operation), metrics.CapabilityCallRefused, string(e.Code), e.Duration.Seconds())
		},
		OnFailed: func(ctx context.Context, e limes.IssuerErrorEvent) {
			record(ctx, capabilityComponentIssuer, string(e.Operation), metrics.CapabilityCallFailed, string(e.Code), e.Duration.Seconds())
		},
	}
}

// verifierEvents counts every token the verifier accepts, refuses or fails
// to check.
func verifierEvents(record capabilityRecorder) limes.VerifierEvents {
	return limes.VerifierEvents{
		OnVerified: func(ctx context.Context, e limes.VerifiedEvent) {
			record(ctx, capabilityComponentVerifier, string(e.Operation), metrics.CapabilityCallOK, "", e.Duration.Seconds())
		},
		OnRefused: func(ctx context.Context, e limes.VerifierErrorEvent) {
			record(ctx, capabilityComponentVerifier, string(e.Operation), metrics.CapabilityCallRefused, string(e.Code), e.Duration.Seconds())
		},
		OnFailed: func(ctx context.Context, e limes.VerifierErrorEvent) {
			record(ctx, capabilityComponentVerifier, string(e.Operation), metrics.CapabilityCallFailed, string(e.Code), e.Duration.Seconds())
		},
	}
}

// revokerEvents counts every revocation the revoker carries out, refuses or
// fails to.
func revokerEvents(record capabilityRecorder) limes.RevokerEvents {
	return limes.RevokerEvents{
		OnRevoked: func(ctx context.Context, e limes.RevokedEvent) {
			record(ctx, capabilityComponentRevoker, string(e.Operation), metrics.CapabilityCallOK, "", e.Duration.Seconds())
		},
		OnRefused: func(ctx context.Context, e limes.RevokerErrorEvent) {
			record(ctx, capabilityComponentRevoker, string(e.Operation), metrics.CapabilityCallRefused, string(e.Code), e.Duration.Seconds())
		},
		OnFailed: func(ctx context.Context, e limes.RevokerErrorEvent) {
			record(ctx, capabilityComponentRevoker, string(e.Operation), metrics.CapabilityCallFailed, string(e.Code), e.Duration.Seconds())
		},
	}
}
