package auth

import (
	"crypto/ecdsa"
	"strings"
	"testing"
	"time"
	"uuid"
)

// refreshToken is an IAM refresh token for user in tenant, edited by edit.
func refreshToken(t *testing.T, key *ecdsa.PrivateKey, now time.Time, user, tenant, jti uuid.UUID, edit func(map[string]any)) string {
	t.Helper()
	c := map[string]any{
		"iss":     "paladin-test",
		"sub":     user.String(),
		"aud":     AudienceIAM,
		"exp":     now.Add(time.Hour).Unix(),
		"tenant":  tenant.String(),
		"jti":     jti.String(),
		"user_id": user.String(),
		"refresh": true,
	}
	if edit != nil {
		edit(c)
	}
	return es256(t, key, c)
}

func refreshDecoder(key *ecdsa.PrivateKey, now time.Time) *RefreshDecoder {
	v := newVerifier(key, now)
	v.ExpectedAudience = AudienceIAM
	return &RefreshDecoder{Verifier: v}
}

// A refresh token names the session it continues: its id, its user and its
// tenant.
func TestDecodeRefresh(t *testing.T) {
	key := ecKey(t)
	now := time.Unix(1_700_000_000, 0)
	user, tenant, jti := uuid.New(), uuid.New(), uuid.New()

	gotJTI, gotUser, gotTenant, err := refreshDecoder(key, now).DecodeRefresh(refreshToken(t, key, now, user, tenant, jti, nil))
	if err != nil {
		t.Fatalf("DecodeRefresh: %v", err)
	}
	if gotJTI != jti || gotUser != user || gotTenant != tenant {
		t.Errorf("decoded (%s, %s, %s), want (%s, %s, %s)", gotJTI, gotUser, gotTenant, jti, user, tenant)
	}
}

// Only a signed, unexpired IAM refresh token that names its session is one.
func TestDecodeRefreshRefuses(t *testing.T) {
	key := ecKey(t)
	now := time.Unix(1_700_000_000, 0)
	user, tenant, jti := uuid.New(), uuid.New(), uuid.New()
	token := func(edit func(map[string]any)) string {
		return refreshToken(t, key, now, user, tenant, jti, edit)
	}
	for _, tc := range []struct {
		name  string
		token string
		want  string
	}{
		{"an access token", token(func(c map[string]any) { delete(c, "refresh") }), "refresh:true"},
		{"no session id", token(func(c map[string]any) { delete(c, "jti") }), "jti"},
		{"a session id that is not one", token(func(c map[string]any) { c["jti"] = "x" }), "jti"},
		{"a user that is not one", token(func(c map[string]any) { c["user_id"] = "x" }), "user_id"},
		{"expired", token(func(c map[string]any) { c["exp"] = now.Add(-time.Hour).Unix() }), "expired"},
		{"another key's signature", refreshToken(t, ecKey(t), now, user, tenant, jti, nil), "signature"},
	} {
		if _, _, _, err := refreshDecoder(key, now).DecodeRefresh(tc.token); err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: err = %v, want one naming %q", tc.name, err, tc.want)
		}
	}
	if _, _, _, err := (&RefreshDecoder{}).DecodeRefresh(token(nil)); err == nil {
		t.Error("a decoder with no verifier decoded a token")
	}
}
