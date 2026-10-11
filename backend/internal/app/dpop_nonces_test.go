package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
)

// nonceLifetime is the lifetime the cases configure.
const nonceLifetime = 5 * time.Minute

// Nonces are off unless asked for; on, they need the key every replica
// shares, and only an environment that may run on an ephemeral key goes
// without one.
func TestDPoPNoncesFollowTheConfig(t *testing.T) {
	sharedKey := strings.Repeat("k", limes.HMACNonceKeyBytes)
	cases := map[string]struct {
		cfg     config.DPoPNonce
		env     string
		nonces  bool
		refused bool
	}{
		"off":                     {cfg: config.DPoPNonce{Key: sharedKey}, env: "prod"},
		"on with the shared key":  {cfg: config.DPoPNonce{Enabled: true, Lifetime: nonceLifetime, Key: sharedKey}, env: "prod", nonces: true},
		"on with no key in dev":   {cfg: config.DPoPNonce{Enabled: true, Lifetime: nonceLifetime}, env: "dev", nonces: true},
		"on with no key in prod":  {cfg: config.DPoPNonce{Enabled: true, Lifetime: nonceLifetime}, env: "prod", refused: true},
		"on with a key too short": {cfg: config.DPoPNonce{Enabled: true, Lifetime: nonceLifetime, Key: "short"}, env: "prod", refused: true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			nonces, err := dpopNonces(tc.cfg, tc.env, zap.NewNop())
			if tc.refused {
				if err == nil {
					t.Fatal("accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if (nonces != nil) != tc.nonces {
				t.Fatalf("nonces = %v, want them: %v", nonces, tc.nonces)
			}
			if nonces == nil {
				return
			}
			n, err := nonces.Nonce(context.Background())
			if err != nil || !nonces.Valid(context.Background(), n) {
				t.Errorf("a nonce issued is refused: %q, %v", n, err)
			}
		})
	}
}
