package auth

import (
	"context"

	"connectrpc.com/connect/v2"
)

// The scopes a user consents to at /oauth/authorize, and what they allow an
// OAuth client to do (ADR-0035). Resource reach is the principal's own and,
// for an agent, its capability's; these decide only whether it may write.
const (
	// OAuthScopeRead admits the RPCs the contract marks NO_SIDE_EFFECTS.
	OAuthScopeRead = "paladin.read"
	// OAuthScopeWrite admits every RPC the principal is otherwise allowed.
	OAuthScopeWrite = "paladin.write"
)

// The RFC 9068 claims an OAuth access token carries its grant in.
const (
	ClaimClientID = "client_id"
	ClaimScope    = "scope"
	// ScopeSeparator joins the scope claim's values (RFC 6749 §3.3).
	ScopeSeparator = " "
)

// ActsForOAuthClient reports whether p holds an OAuth client's grant.
func (p *Principal) ActsForOAuthClient() bool { return p.OAuthClientID != "" }

// MayWrite reports whether p may call an RPC that changes state: always,
// unless p acts for an OAuth client whose grant lacks OAuthScopeWrite.
func (p *Principal) MayWrite() bool {
	if !p.ActsForOAuthClient() {
		return true
	}
	for _, s := range p.OAuthScopes {
		if s == OAuthScopeWrite {
			return true
		}
	}
	return false
}

// RequireOAuthWriteScope refuses an OAuth client without OAuthScopeWrite
// every RPC the contract does not mark NO_SIDE_EFFECTS. The contract is the
// classification: an RPC that reads says so in its idempotency_level, and one
// that does not is treated as a write.
//
// Use after every interceptor that establishes a principal. A request with no
// principal passes, for the RPCs that authenticate in the handler (Login).
func RequireOAuthWriteScope() connect.ServerInterceptor {
	return func(next connect.ServerFunc) connect.ServerFunc {
		return func(ctx context.Context, spec connect.Spec, stream connect.ServerStream) error {
			if err := checkOAuthWriteScope(ctx, spec); err != nil {
				return err
			}
			return next(ctx, spec, stream)
		}
	}
}

func checkOAuthWriteScope(ctx context.Context, spec connect.Spec) error {
	if spec.IdempotencyLevel == connect.IdempotencyNoSideEffects {
		return nil
	}
	p, err := PrincipalFromContext(ctx)
	if err != nil || p.MayWrite() {
		return nil
	}
	return connect.Errorf(connect.CodePermissionDenied,
		"OAuth client %q was not granted %q, which %s requires", p.OAuthClientID, OAuthScopeWrite, spec.Procedure)
}
