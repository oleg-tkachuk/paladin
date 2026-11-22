package auth

import (
	"errors"

	"connectrpc.com/connect/v2"
)

// Why a bearer token is refused, as the caller is told. The verifiers wrap
// one of them around the JWT library's own error, which stays the cause for
// the failure log and never reaches the caller: it quotes the token's parser
// and the library's internals, nothing the caller can act on.
var (
	ErrBearerMalformed        = errors.New("malformed bearer token")
	ErrBearerUnknownKey       = errors.New("bearer token signed by an unknown key")
	ErrBearerSignature        = errors.New("bearer token signature invalid")
	ErrBearerExpired          = errors.New("bearer token expired")
	ErrBearerNotYetValid      = errors.New("bearer token not yet valid")
	ErrBearerIssuerMismatch   = errors.New("bearer token issuer mismatch")
	ErrBearerAudienceMismatch = errors.New("bearer token audience mismatch")
	ErrBearerClaims           = errors.New("bearer token claims invalid")
)

// bearerRefusals is every reason a caller is told, in the order a refusal is
// matched against them.
var bearerRefusals = []error{
	ErrBearerMalformed,
	ErrBearerUnknownKey,
	ErrBearerSignature,
	ErrBearerExpired,
	ErrBearerNotYetValid,
	ErrBearerIssuerMismatch,
	ErrBearerAudienceMismatch,
	ErrBearerClaims,
}

// bearerUnverified is what a caller is told of a bearer token that failed for
// none of bearerRefusals: a key set that would not load, a key of a kind the
// verifier does not know. The cause is the server's and is logged instead.
const bearerUnverified = "bearer token could not be verified" // #nosec G101 -- a refusal's message, not a credential

// BearerRefusals is every message a refused bearer token may be answered
// with: each reason, and the sentence for a failure that is none of them.
func BearerRefusals() []string {
	out := make([]string, 0, len(bearerRefusals)+1)
	for _, r := range bearerRefusals {
		out = append(out, r.Error())
	}
	return append(out, bearerUnverified)
}

// bearerRefusal is the answer to a bearer token the verifier refused with
// err: Unauthenticated, with the reason err wraps as its whole message and
// err itself as the cause, which the failure log records.
func bearerRefusal(err error) *connect.Error {
	return connect.NewError(connect.CodeUnauthenticated, bearerRefusalText(err)).WithCause(err)
}

// bearerRefusalText is the reason err wraps, else bearerUnverified.
func bearerRefusalText(err error) string {
	for _, r := range bearerRefusals {
		if errors.Is(err, r) {
			return r.Error()
		}
	}
	return bearerUnverified
}
