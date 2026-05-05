package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
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
		return uuid.Nil, uuid.Nil, uuid.Nil, errors.New("auth: no verifier configured")
	}
	// Verify signature/exp via the standard verifier; we re-use Principal
	// extraction to confirm the audience matches IAM.
	p, err := d.Verifier.Verify(context.Background(), token)
	if err != nil {
		return uuid.Nil, uuid.Nil, uuid.Nil, fmt.Errorf("refresh token: %w", err)
	}
	if p.Audience != AudienceIAM {
		return uuid.Nil, uuid.Nil, uuid.Nil, errors.New("refresh token: wrong audience")
	}

	// Re-parse claim payload for the refresh-specific fields. Verifier already
	// validated signature; we just need user_id / jti.
	claims, err := decodeClaims(token)
	if err != nil {
		return uuid.Nil, uuid.Nil, uuid.Nil, err
	}
	if !claims.Refresh {
		return uuid.Nil, uuid.Nil, uuid.Nil, errors.New("refresh token: missing `refresh:true` claim")
	}
	jtiStr, _ := claims.JTI.(string)
	if jtiStr == "" {
		return uuid.Nil, uuid.Nil, uuid.Nil, errors.New("refresh token: missing jti")
	}
	jti, err = uuid.Parse(jtiStr)
	if err != nil {
		return uuid.Nil, uuid.Nil, uuid.Nil, fmt.Errorf("refresh token: jti: %w", err)
	}
	userID, err = uuid.Parse(claims.UserID)
	if err != nil {
		return uuid.Nil, uuid.Nil, uuid.Nil, fmt.Errorf("refresh token: user_id: %w", err)
	}
	if p.TenantID != uuid.Nil {
		tenantID = p.TenantID
	}
	return jti, userID, tenantID, nil
}

type refreshClaims struct {
	JTI     any    `json:"jti"`
	UserID  string `json:"user_id"`
	Refresh bool   `json:"refresh"`
}

func decodeClaims(token string) (refreshClaims, error) {
	parts := splitOnDot(token)
	if len(parts) != 3 {
		return refreshClaims{}, errors.New("refresh token: malformed")
	}
	body, err := b64Decode(parts[1])
	if err != nil {
		return refreshClaims{}, fmt.Errorf("refresh token: claims decode: %w", err)
	}
	var c refreshClaims
	if err := json.Unmarshal(body, &c); err != nil {
		return refreshClaims{}, fmt.Errorf("refresh token: claims parse: %w", err)
	}
	return c, nil
}

func splitOnDot(s string) []string {
	out := make([]string, 0, 3)
	last := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '.' {
			out = append(out, s[last:i])
			last = i + 1
		}
	}
	out = append(out, s[last:])
	return out
}
