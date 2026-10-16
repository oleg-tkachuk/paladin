package oauth

import (
	"errors"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
)

func TestSeedClient(t *testing.T) {
	const secret = "seed-client-secret"

	public, err := SeedClient(config.OAuthSeedClient{ClientID: "agent", Public: true, AllowedScopes: []string{auth.OAuthScopeRead}})
	if err != nil {
		t.Fatalf("public: %v", err)
	}
	if !public.Public || public.SecretHash != nil || public.AllowedScopes[0] != auth.OAuthScopeRead {
		t.Errorf("public client = %+v, want public with no hash and its scopes", public)
	}

	conf, err := SeedClient(config.OAuthSeedClient{ClientID: "mcp", ClientSecret: secret})
	if err != nil {
		t.Fatalf("confidential: %v", err)
	}
	if conf.Public {
		t.Error("confidential client seeded as public")
	}
	if err := auth.CheckApiKeySecret(conf.SecretHash, secret); err != nil {
		t.Errorf("the stored hash does not verify the configured secret: %v", err)
	}
	if string(conf.SecretHash) == secret {
		t.Error("the secret is stored in the clear")
	}

	if _, err := SeedClient(config.OAuthSeedClient{ClientID: "mcp"}); !errors.Is(err, ErrSeedSecretUnresolved) {
		t.Errorf("confidential with no secret: err = %v, want ErrSeedSecretUnresolved", err)
	}
}
