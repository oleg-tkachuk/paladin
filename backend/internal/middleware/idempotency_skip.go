package middleware

import (
	"github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/iam/v1/paladiniamv1connect"
)

// CredentialMintingProcedures must never be memoized by the idempotency
// interceptor, whatever header a client sends.
//
// Replaying a cached response is correct for a resource create — the caller
// wanted one bucket and gets the one bucket. It is wrong for an RPC whose
// response IS a credential, because a credential is not a description of state
// that can be re-read; it is a one-time grant with its own lifecycle.
//
// RefreshToken is the sharp case. Refresh tokens rotate: presenting one
// invalidates it and returns a successor, and presenting a rotated token again
// is treated as theft (RFC 6819) — the handler revokes the whole family. A
// memoized RefreshToken response would hand a second caller the ALREADY
// ROTATED pair, which is both useless to them and indistinguishable, from the
// server's side, from the replay it is built to detect. Login is milder but the
// same shape: two logins that share a key would share an access token and its
// expiry, so revoking one session silently kills the other.
//
// This list was empty until the console started sending Idempotency-Key on
// every mutation rather than only on Create*/Issue*. Before that no client sent
// one here, so the hazard was unreachable — which is not the same as absent,
// and a third-party integrator reading "send Idempotency-Key on mutations"
// would have reached it first.
//
// Delegate and Issue are deliberately NOT here: a capability token is minted
// against an explicit scope the caller names, and collapsing a double-submit
// onto one capability is exactly what an idempotency key is for.
var CredentialMintingProcedures = map[string]bool{
	paladiniamv1connect.AuthServiceLoginProcedure:            true,
	paladiniamv1connect.AuthServiceRefreshTokenProcedure:     true,
	paladiniamv1connect.AuthServiceExchangeAudienceProcedure: true,
	paladiniamv1connect.AuthServiceSwitchTenantProcedure:     true,
}
