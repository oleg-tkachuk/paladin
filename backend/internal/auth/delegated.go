package auth

import (
	"errors"
	"slices"

	"github.com/oleg-tkachuk/limes"
)

// A delegated capability is one the MCP server exchanged a caller's token
// for (ADR-0041): it acts for the user, and the plane authorizes the user
// narrowed by its caveats. Its operations say what the user consented to.

// DelegationIssuerSubject names the MCP server as the issuer of every
// delegated capability.
const DelegationIssuerSubject = AudienceMCP

// The operations that carry a grant's consent on a delegated capability.
const (
	OpDelegatedRead  = limes.Op("paladin:read")
	OpDelegatedWrite = limes.Op("paladin:write")
)

// delegatedReadOps are what OAuthScopeRead admits; delegatedWriteOps are
// what OAuthScopeWrite adds. Neither holds limes.OpShare: a delegated
// capability carries its user's roles, so it is never delegated further.
var (
	delegatedReadOps  = []limes.Op{OpDelegatedRead, limes.OpGet, limes.OpList, OpPresign}
	delegatedWriteOps = []limes.Op{OpDelegatedWrite, limes.OpPut, limes.OpDelete, limes.OpManage, OpTag}
)

// ErrNoConsent is a grant that consents to neither OAuthScopeRead nor
// OAuthScopeWrite.
var ErrNoConsent = errors.New("auth: the grant consents to neither " + OAuthScopeRead + " nor " + OAuthScopeWrite)

// DelegationIssuerSuffix makes the delegation issuer's name from the
// capability issuer's: a delegated capability's `iss` is
// "<issuer_name>" + DelegationIssuerSuffix, signed under a kid that signs
// for nothing else, so it marks a delegated capability.
const DelegationIssuerSuffix = "/mcp"

// DelegationIssuerName is the `iss` of a delegated capability, given the
// capability issuer's name.
func DelegationIssuerName(issuerName string) string { return issuerName + DelegationIssuerSuffix }

// DelegationIssuer is the IssuedBy recorded with every delegated capability.
func DelegationIssuer() limes.Principal {
	return limes.Principal{Type: limes.PrincipalTypeService, Subject: DelegationIssuerSubject}
}

// DelegatedOps is the operations a delegated capability gets for the scopes
// its user consented to. Write implies read, as it does for a token.
func DelegatedOps(scopes []string) ([]limes.Op, error) {
	write := slices.Contains(scopes, OAuthScopeWrite)
	if !write && !slices.Contains(scopes, OAuthScopeRead) {
		return nil, ErrNoConsent
	}
	ops := slices.Clone(delegatedReadOps)
	if write {
		ops = append(ops, delegatedWriteOps...)
	}
	return ops, nil
}

// DelegatedScopes reads the consented scopes back from a delegated
// capability's operations, for the principal it establishes.
func DelegatedScopes(ops []limes.Op) []string {
	scopes := []string{OAuthScopeRead}
	if slices.Contains(ops, OpDelegatedWrite) {
		scopes = append(scopes, OAuthScopeWrite)
	}
	return scopes
}
