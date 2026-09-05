package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// JWTVerifier.Verify decides whether every request to every plane is
// authenticated, and it had no test file at all.
//
// Mutation testing is what said so. Flipping `c.Exp > 0 && now().After(…)` to
// `||` — which rejects EVERY token that carries an exp claim, i.e. all of them
// — left `go test ./internal/auth/` green. So did inverting the three-segment
// check, the nbf check, and the claims unmarshal. Four guards on the
// authentication path, none of them held by anything.
//
// The happy path is the one that was missing. Every existing test in this
// package drives a rejection, and a verifier that rejects everything passes all
// of them.

func hs256(t *testing.T, secret []byte, claims map[string]any) string {
	t.Helper()
	enc := func(v any) string {
		b, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return base64.RawURLEncoding.EncodeToString(b)
	}
	head := enc(map[string]string{"alg": "HS256", "typ": "JWT"})
	body := enc(claims)
	signing := head + "." + body
	mac := hmac.New(sha256.New, secret)
	mac.Write([]byte(signing))
	return signing + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func newVerifier(secret []byte, now time.Time) *JWTVerifier {
	return &JWTVerifier{
		Key:              secret,
		ExpectedIssuer:   "paladin-test",
		ExpectedAudience: "paladin-data",
		Leeway:           time.Second,
		Now:              func() time.Time { return now },
	}
}

func baseClaims(now time.Time) map[string]any {
	return map[string]any{
		"iss":    "paladin-test",
		"sub":    "subject-1",
		"aud":    "paladin-data",
		"exp":    now.Add(time.Hour).Unix(),
		"nbf":    now.Add(-time.Hour).Unix(),
		"tenant": "11111111-1111-1111-1111-111111111111",
		"roles":  []string{"platform.admin"},
	}
}

// THE MISSING TEST. A well-formed, unexpired, correctly signed token must
// verify — and the principal must carry what the claims said, since a verifier
// that returns an empty principal is a different failure with the same shape.
func TestValidTokenVerifies(t *testing.T) {
	secret := []byte("dev-secret-change-me-32-bytes-min")
	now := time.Unix(1_700_000_000, 0)

	p, err := newVerifier(secret, now).Verify(context.Background(),
		hs256(t, secret, baseClaims(now)))
	if err != nil {
		t.Fatalf("a valid token was rejected: %v", err)
	}
	if p == nil {
		t.Fatal("no principal returned for a valid token")
	}
	if p.Subject != "subject-1" {
		t.Errorf("subject = %q, want subject-1", p.Subject)
	}
	if p.Audience != "paladin-data" {
		t.Errorf("audience = %q, want paladin-data", p.Audience)
	}
	if p.TenantID.String() != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("tenant = %v, want the one in the claim", p.TenantID)
	}
}

// Each rejection, and each one on its own so a verifier that rejects for the
// wrong reason is not credited for rejecting.
func TestVerifyRejects(t *testing.T) {
	secret := []byte("dev-secret-change-me-32-bytes-min")
	now := time.Unix(1_700_000_000, 0)

	cases := []struct {
		name  string
		token func() string
		want  string
	}{
		{"expired", func() string {
			c := baseClaims(now)
			c["exp"] = now.Add(-time.Hour).Unix()
			return hs256(t, secret, c)
		}, "expired"},
		{"not yet valid", func() string {
			c := baseClaims(now)
			c["nbf"] = now.Add(time.Hour).Unix()
			return hs256(t, secret, c)
		}, "not yet valid"},
		{"wrong issuer", func() string {
			c := baseClaims(now)
			c["iss"] = "someone-else"
			return hs256(t, secret, c)
		}, "issuer"},
		{"wrong audience", func() string {
			c := baseClaims(now)
			c["aud"] = "paladin-admin"
			return hs256(t, secret, c)
		}, "audience"},
		{"bad signature", func() string {
			return hs256(t, []byte("a-different-secret-of-the-same-len"), baseClaims(now))
		}, ""},
		{"two segments, not three", func() string {
			parts := strings.Split(hs256(t, secret, baseClaims(now)), ".")
			return parts[0] + "." + parts[1]
		}, ""},
		{"claims are not JSON", func() string {
			parts := strings.Split(hs256(t, secret, baseClaims(now)), ".")
			parts[1] = base64.RawURLEncoding.EncodeToString([]byte("not json"))
			return strings.Join(parts, ".")
		}, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p, err := newVerifier(secret, now).Verify(context.Background(), c.token())
			if err == nil {
				t.Fatalf("accepted a token that is %s (principal %+v)", c.name, p)
			}
			if c.want != "" && !strings.Contains(err.Error(), c.want) {
				t.Errorf("error %q does not mention %q — rejecting for the wrong "+
					"reason passes a test that only checks for failure", err, c.want)
			}
		})
	}
}

// `aud` is a string OR an array in the spec, and Paladin accepts both. The
// array branch had no coverage: a token whose audience is ["paladin-data"]
// went down a path nothing exercised, and inverting its unmarshal check
// survived the mutation run.
func TestAudienceAsArray(t *testing.T) {
	secret := []byte("dev-secret-change-me-32-bytes-min")
	now := time.Unix(1_700_000_000, 0)

	t.Run("matches when the wanted audience is in the array", func(t *testing.T) {
		c := baseClaims(now)
		c["aud"] = []string{"paladin-admin", "paladin-data"}
		if _, err := newVerifier(secret, now).Verify(context.Background(),
			hs256(t, secret, c)); err != nil {
			t.Errorf("an audience array containing the plane was rejected: %v", err)
		}
	})

	t.Run("rejects when it is not", func(t *testing.T) {
		c := baseClaims(now)
		c["aud"] = []string{"paladin-admin", "paladin-iam"}
		if _, err := newVerifier(secret, now).Verify(context.Background(),
			hs256(t, secret, c)); err == nil {
			t.Error("an audience array without this plane was accepted — a token " +
				"minted for another plane must not open this one")
		}
	})
}

// The leeway exists so a small clock skew does not log everyone out. Asserted
// on both sides of the boundary, since a leeway that swallows an hour is as
// wrong as one that swallows nothing.
func TestExpiryLeeway(t *testing.T) {
	secret := []byte("dev-secret-change-me-32-bytes-min")
	now := time.Unix(1_700_000_000, 0)
	exp := now.Add(-2 * time.Second)

	c := baseClaims(now)
	c["exp"] = exp.Unix()
	tok := hs256(t, secret, c)

	v := newVerifier(secret, now)
	v.Leeway = 5 * time.Second
	if _, err := v.Verify(context.Background(), tok); err != nil {
		t.Errorf("a token 2s past expiry was rejected with 5s of leeway: %v", err)
	}
	v.Leeway = time.Second
	if _, err := v.Verify(context.Background(), tok); err == nil {
		t.Error("a token 2s past expiry was accepted with only 1s of leeway")
	}
}
