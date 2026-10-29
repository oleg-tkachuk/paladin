package delegation

import (
	"context"
	"errors"
	"fmt"

	"github.com/oleg-tkachuk/limes/exchange"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
)

// subjectClaimTenant carries the token's tenant from the SubjectVerifier to
// the delegation policy, which holds the user to it.
const subjectClaimTenant = "tenant"

// errNoClient is a paladin-mcp token no OAuth client was issued: there is
// no grant, and so no consent, to exchange it under.
var errNoClient = errors.New("the token names no OAuth client")

// SubjectVerifier reads a caller's paladin-mcp token for limes' exchange
// (ADR-0041). Paladin's IAM tokens are its own HS256 JWTs, not RFC 9068
// `at+jwt`, so the verifier the MCP edge already uses checks them, and this
// gives the exchange what it reads.
type SubjectVerifier struct {
	tokens auth.TokenVerifier
	issuer string
}

// NewSubjectVerifier reads tokens that tokens verifies, which issuer signs.
func NewSubjectVerifier(tokens auth.TokenVerifier, issuer string) *SubjectVerifier {
	return &SubjectVerifier{tokens: tokens, issuer: issuer}
}

// VerifySubject implements exchange.SubjectVerifier. Every refusal is
// exchange.ErrInvalidGrant: the verifier holds no state that could fail.
func (v *SubjectVerifier) VerifySubject(ctx context.Context, token string) (exchange.Subject, error) {
	p, err := v.tokens.Verify(ctx, token)
	if err != nil {
		return exchange.Subject{}, fmt.Errorf("%w: %w", exchange.ErrInvalidGrant, err)
	}
	if !p.ActsForOAuthClient() {
		return exchange.Subject{}, fmt.Errorf("%w: %w", exchange.ErrInvalidGrant, errNoClient)
	}
	return exchange.Subject{
		Issuer:    v.issuer,
		ID:        p.TokenID,
		Subject:   p.Subject,
		ClientID:  p.OAuthClientID,
		Audience:  p.Audiences,
		Scopes:    p.OAuthScopes,
		ExpiresAt: p.ExpiresAt,
		Claims:    map[string]any{subjectClaimTenant: p.TenantID.String()},
	}, nil
}
