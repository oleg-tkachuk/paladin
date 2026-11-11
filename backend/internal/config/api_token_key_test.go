package config

import "testing"

// The API-token pepper is a secret of its own: it is no longer derived from
// the session signing key (ADR-0047), so enabling API tokens without one is
// refused when the config is loaded rather than when the first token is.
func TestValidate_APITokenNeedsItsOwnHMACKey(t *testing.T) {
	c := minimalValidConfig()
	c.APIToken.Enabled = true
	c.Auth.SigningKey = "a-session-key-that-no-longer-seeds-the-pepper"
	mustReject(t, c, "api_token: hmac_key or hmac_key_secret is required")
}

func TestValidate_APITokenHMACKeySources(t *testing.T) {
	for name, set := range map[string]func(*APIToken){
		"disabled, no key": func(*APIToken) {},
		"inline":           func(a *APIToken) { a.Enabled, a.HMACKey = true, "pepper" },
		"secret":           func(a *APIToken) { a.Enabled, a.HMACKeySecret = true, &SecretRef{Name: "p", Key: "k"} },
	} {
		t.Run(name, func(t *testing.T) {
			c := minimalValidConfig()
			set(&c.APIToken)
			if err := c.Validate(); err != nil {
				t.Fatalf("Validate(): %v", err)
			}
		})
	}
}
