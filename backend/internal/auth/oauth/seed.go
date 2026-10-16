package oauth

import (
	"errors"
	"fmt"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
)

// ErrSeedSecretUnresolved is returned for a confidential seed client whose
// secret is empty at boot: its client_secret_ref was never resolved (the
// process runs outside a cluster) or the Secret holds nothing. Seeding it
// without a hash would let anyone authenticate as it.
var ErrSeedSecretUnresolved = errors.New("oauth: a confidential seed client has no secret")

// SeedClient is the registration a configured seed client stands for. A
// confidential client's secret is stored only as its hash.
func SeedClient(sc config.OAuthSeedClient) (Client, error) {
	c := Client{
		ClientID:         sc.ClientID,
		RedirectURIs:     sc.RedirectURIs,
		AllowedScopes:    sc.AllowedScopes,
		AllowedAudiences: sc.AllowedAudiences,
		Public:           sc.Public,
	}
	if sc.Public {
		return c, nil
	}
	if sc.ClientSecret == "" {
		return Client{}, fmt.Errorf("%w: %s", ErrSeedSecretUnresolved, sc.ClientID)
	}
	hash, err := auth.HashApiKeySecret(sc.ClientSecret)
	if err != nil {
		return Client{}, fmt.Errorf("oauth: hash the secret of %s: %w", sc.ClientID, err)
	}
	c.SecretHash = hash
	return c, nil
}
