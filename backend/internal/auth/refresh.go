package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"uuid"

	"github.com/lestrrat-go/jwx/v3/jws"
)

// RefreshDecoder verifies a refresh token's signature against the IAM-audience
// verifier and extracts (jti, user_id, tenant_id). Returns ErrTokenInvalid for
// any verification or claim-shape failure.
type RefreshDecoder struct {
	Verifier *JWTVerifier // must have ExpectedAudience=AudienceIAM
}

// DecodeRefresh implements iam/authh.RefreshTokenDecoder.
func (d *RefreshDecoder) DecodeRefresh(token string) (jti, userID, tenantID uuid.UUID, err error) {
	if d.Verifier == nil {
		return uuid.Nil(), uuid.Nil(), uuid.Nil(), errors.New("auth: no verifier configured")
	}
	// Verify signature/exp via the standard verifier; we re-use Principal
	// extraction to confirm the audience matches IAM.
	p, err := d.Verifier.Verify(context.Background(), token)
	if err != nil {
		return uuid.Nil(), uuid.Nil(), uuid.Nil(), fmt.Errorf("refresh token: %w", err)
	}
	if p.Audience != AudienceIAM {
		return uuid.Nil(), uuid.Nil(), uuid.Nil(), errors.New("refresh token: wrong audience")
	}

	// Re-parse claim payload for the refresh-specific fields. Verifier already
	// validated signature; we just need user_id / jti.
	claims, err := decodeClaims(token)
	if err != nil {
		return uuid.Nil(), uuid.Nil(), uuid.Nil(), err
	}
	if !claims.Refresh {
		return uuid.Nil(), uuid.Nil(), uuid.Nil(), errors.New("refresh token: missing `refresh:true` claim")
	}
	jtiStr, _ := claims.JTI.(string)
	if jtiStr == "" {
		return uuid.Nil(), uuid.Nil(), uuid.Nil(), errors.New("refresh token: missing jti")
	}
	jti, err = uuid.Parse(jtiStr)
	if err != nil {
		return uuid.Nil(), uuid.Nil(), uuid.Nil(), fmt.Errorf("refresh token: jti: %w", err)
	}
	userID, err = uuid.Parse(claims.UserID)
	if err != nil {
		return uuid.Nil(), uuid.Nil(), uuid.Nil(), fmt.Errorf("refresh token: user_id: %w", err)
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
		return refreshClaims{}, fmt.Errorf("refresh token: malformed: %w", err)
	}
	var c refreshClaims
	if err := json.Unmarshal(msg.Payload(), &c); err != nil {
		return refreshClaims{}, fmt.Errorf("refresh token: claims parse: %w", err)
	}
	return c, nil
}
