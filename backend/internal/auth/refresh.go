package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"uuid"

	"connectrpc.com/connect/v2"
	"github.com/lestrrat-go/jwx/v4/jws"
)

// errNoRefreshVerifier is a decoder built without a verifier: the server's
// fault, answered as a token that could not be verified.
var errNoRefreshVerifier = errors.New("refresh decoder has no verifier")

// The credential a refusal names: a refresh token is a bearer JWT, refused
// for the same reasons, and told so under its own name.
const (
	bearerCredentialName  = "bearer token"
	refreshCredentialName = "refresh token"
)

// RefreshRefusal is the answer to a refresh token DecodeRefresh refused with
// err: Unauthenticated — Unavailable when the key set would not load — the
// reason it wraps naming the refresh token, and err as the cause for the
// failure log.
func RefreshRefusal(err error) *connect.Error {
	msg := strings.Replace(bearerRefusalText(err), bearerCredentialName, refreshCredentialName, 1)
	return connect.NewError(bearerRefusalCode(err), msg).WithCause(err)
}

// RefreshDecoder verifies a refresh token's signature against the IAM-audience
// verifier and extracts (jti, user_id, tenant_id). Every refusal wraps one of
// the bearer refusals (ErrBearerMalformed, ErrBearerClaims, …), which
// RefreshRefusal answers.
type RefreshDecoder struct {
	Verifier TokenVerifier // must have ExpectedAudience=AudienceIAM
}

// DecodeRefresh implements iam/authh.RefreshTokenDecoder.
func (d *RefreshDecoder) DecodeRefresh(token string) (jti, userID, tenantID uuid.UUID, err error) {
	if d.Verifier == nil {
		return uuid.Nil(), uuid.Nil(), uuid.Nil(), errNoRefreshVerifier
	}
	// Verify signature/exp via the standard verifier; we re-use Principal
	// extraction to confirm the audience matches IAM.
	p, err := d.Verifier.Verify(context.Background(), token)
	if err != nil {
		return uuid.Nil(), uuid.Nil(), uuid.Nil(), err
	}
	if p.Audience != AudienceIAM {
		return uuid.Nil(), uuid.Nil(), uuid.Nil(), fmt.Errorf("%w: issued for %q", ErrBearerAudienceMismatch, p.Audience)
	}

	// Re-parse claim payload for the refresh-specific fields. Verifier already
	// validated signature; we just need user_id / jti.
	claims, err := decodeClaims(token)
	if err != nil {
		return uuid.Nil(), uuid.Nil(), uuid.Nil(), err
	}
	if !claims.Refresh {
		return uuid.Nil(), uuid.Nil(), uuid.Nil(), fmt.Errorf("%w: missing `refresh:true`", ErrBearerClaims)
	}
	jtiStr, _ := claims.JTI.(string)
	if jtiStr == "" {
		return uuid.Nil(), uuid.Nil(), uuid.Nil(), fmt.Errorf("%w: missing jti", ErrBearerClaims)
	}
	jti, err = uuid.Parse(jtiStr)
	if err != nil {
		return uuid.Nil(), uuid.Nil(), uuid.Nil(), fmt.Errorf("%w: jti: %w", ErrBearerClaims, err)
	}
	userID, err = uuid.Parse(claims.UserID)
	if err != nil {
		return uuid.Nil(), uuid.Nil(), uuid.Nil(), fmt.Errorf("%w: user_id: %w", ErrBearerClaims, err)
	}
	if p.TenantID != uuid.Nil() {
		tenantID = p.TenantID
	}
	return jti, userID, tenantID, nil
}

type refreshClaims struct {
	JTI     any    `json:"jti"`
	UserID  string `json:"user_id"`
	Refresh bool   `json:"refresh"`
}

// decodeClaims reads the refresh-specific claims of a token whose signature
// the verifier has already checked.
func decodeClaims(token string) (refreshClaims, error) {
	msg, err := jws.ParseString(token)
	if err != nil {
		return refreshClaims{}, fmt.Errorf("%w: %w", ErrBearerMalformed, err)
	}
	var c refreshClaims
	if err := json.Unmarshal(msg.Payload(), &c); err != nil {
		return refreshClaims{}, fmt.Errorf("%w: parse: %w", ErrBearerClaims, err)
	}
	return c, nil
}
