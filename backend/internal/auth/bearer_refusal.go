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

// ErrKeySetUnavailable is a key set that could not be fetched when a token
// named a key the cache does not hold: the server could not decide, and the
// token may be good. It is answered Unavailable, so the caller retries.
var ErrKeySetUnavailable = errors.New("the key set that verifies bearer tokens could not be fetched")

// bearerKeysUnavailable is what a caller is told of ErrKeySetUnavailable.
const bearerKeysUnavailable = "the keys that verify a bearer token could not be loaded; retry the call" // #nosec G101 -- a refusal's message, not a credential

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
// none of bearerRefusals and is not ErrKeySetUnavailable: a key of a kind the
// verifier does not know. The cause is the server's and is logged instead.
const bearerUnverified = "bearer token could not be verified" // #nosec G101 -- a refusal's message, not a credential

// BearerRefusals is every message a refused bearer token may be answered
// with: each reason, the sentence for a key set that would not load, and the
// one for a failure that is none of them.
func BearerRefusals() []string {
	out := make([]string, 0, len(bearerRefusals)+2)
	for _, r := range bearerRefusals {
		out = append(out, r.Error())
	}
	return append(out, bearerKeysUnavailable, bearerUnverified)
}

// bearerRefusal is the answer to a bearer token the verifier refused with
// err: bearerRefusalCode, with the reason err wraps as its whole message and
// err itself as the cause, which the failure log records.
func bearerRefusal(err error) *connect.Error {
	return connect.NewError(bearerRefusalCode(err), bearerRefusalText(err)).WithCause(err)
}

// bearerRefusalCode is Unavailable for a key set that would not load — the
// server's dependency failed, which the failure log logs at Error — and
// Unauthenticated for every token refused.
func bearerRefusalCode(err error) connect.Code {
	if errors.Is(err, ErrKeySetUnavailable) {
		return connect.CodeUnavailable
	}
	return connect.CodeUnauthenticated
}

// bearerRefusalText is the reason err wraps, else bearerKeysUnavailable for
// a key set that would not load, else bearerUnverified.
func bearerRefusalText(err error) string {
	for _, r := range bearerRefusals {
		if errors.Is(err, r) {
			return r.Error()
		}
	}
	if errors.Is(err, ErrKeySetUnavailable) {
		return bearerKeysUnavailable
	}
	return bearerUnverified
}
