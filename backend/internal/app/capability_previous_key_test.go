package app

import (
	"errors"
	"path/filepath"
	"testing"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/biscuit"
	"github.com/oleg-tkachuk/limes/jwks"
	"github.com/oleg-tkachuk/limes/memstore"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
)

// namedPreviousKID is a kid an operator named through signing_key_kid.
const namedPreviousKID = jwks.KeyIDPrefix + "2026-10-signing"

// writePreviousKey writes key's public half as the PKIX PEM an operator keeps
// once the key no longer signs, and returns its path.
func writePreviousKey(t *testing.T, key limes.SigningKey) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "previous.pub.pem")
	if err := limes.WritePublicKey(path, key.Public()); err != nil {
		t.Fatal(err)
	}
	return path
}

func generateKey(t *testing.T) limes.SigningKey {
	t.Helper()
	key, err := limes.GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	return key
}

// A previous key is loaded under the kid limes derives from it, or under the
// kid it names when signing_key_kid named one.
func TestPreviousCapabilityKeysLoaded(t *testing.T) {
	key := generateKey(t)
	path := writePreviousKey(t, key)
	for name, tc := range map[string]struct{ kid, want string }{
		"derived kid": {"", jwks.KeyID(key.Public())},
		"named kid":   {namedPreviousKID, namedPreviousKID},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := previousCapabilityKeys([]config.CapabilityPublicKey{{Path: path, KID: tc.kid}})
			if err != nil {
				t.Fatal(err)
			}
			if len(got) != 1 || got[0].KeyID != tc.want || !got[0].Key.Equal(key.Public()) {
				t.Fatalf("loaded %+v, want the key at %s under %q", got, path, tc.want)
			}
		})
	}
}

// A previous key that cannot be read, is a private key, or names a kid limes
// would refuse on every token stops the start rather than verifying nothing.
func TestPreviousCapabilityKeysRefused(t *testing.T) {
	signingPath, _ := writeSigningKey(t)
	public := writePreviousKey(t, generateKey(t))
	for name, k := range map[string]config.CapabilityPublicKey{
		"missing file":    {Path: filepath.Join(t.TempDir(), "absent.pem")},
		"a private key":   {Path: signingPath},
		"not a limes kid": {Path: public, KID: "2026-10-signing"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := previousCapabilityKeys([]config.CapabilityPublicKey{k}); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}

// rotationFixture is a store both key generations issue into, the issuers of
// the key rotated out, and the issuers that sign with its successor while
// verifying under it.
type rotationFixture struct {
	store         *memstore.Store
	old, rotated  *capabilityIssuers
	oldKey        limes.SigningKey
	rotatedVerify *limes.Verifier
}

func newRotationFixture(t *testing.T) rotationFixture {
	t.Helper()
	store, err := memstore.NewStore(memstore.StoreConfig{})
	if err != nil {
		t.Fatal(err)
	}
	oldKey := generateKey(t)
	old, err := newCapabilityIssuers(oldKey, nil, store, store, testIssuerName, nil, testCapTTL, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	previous, err := previousCapabilityKeys([]config.CapabilityPublicKey{{Path: writePreviousKey(t, oldKey)}})
	if err != nil {
		t.Fatal(err)
	}
	rotated, err := newCapabilityIssuers(generateKey(t), previous, store, store, testIssuerName, nil, testCapTTL, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	return rotationFixture{store: store, old: old, rotated: rotated, oldKey: oldKey, rotatedVerify: verifierOf(t, rotated, store)}
}

func verifierOf(t *testing.T, issuers *capabilityIssuers, store *memstore.Store) *limes.Verifier {
	t.Helper()
	v, err := limes.NewVerifier(limes.VerifierConfig{
		Keys: issuers.keys, Revocations: store, TrustedIssuers: issuers.trusted, KeyIssuers: issuers.keyIssuers,
		Biscuit: &limes.BiscuitConfig{Form: biscuit.Form(), Revocations: store},
	})
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func (f rotationFixture) verify(t *testing.T, v *limes.Verifier, token string) (limes.VerifiedCapability, error) {
	t.Helper()
	return v.Verify(t.Context(), limes.VerifyRequest{Token: token, Audience: testCapAudience})
}

// Every form a capability the rotated-out key signed is held in — the JWT, its
// Biscuit, a copy narrowed offline, and the delegation issuer's — still
// verifies once a new key signs, and is read as its own issuer's.
func TestAPreviousKeyStillVerifies(t *testing.T) {
	f := newRotationFixture(t)
	jwt, err := issueWith(t, f.old.issuer)
	if err != nil {
		t.Fatal(err)
	}
	verified, err := f.verify(t, verifierOf(t, f.old, f.store), jwt)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := f.old.issuer.Biscuit(t.Context(), limes.BiscuitRequest{Capability: verified})
	if err != nil {
		t.Fatal(err)
	}
	narrowed, err := biscuit.Attenuate(sealed.Token, limes.Attenuation{Ops: []limes.Op{limes.OpGet}})
	if err != nil {
		t.Fatal(err)
	}
	delegated, err := issueWith(t, f.old.delegation)
	if err != nil {
		t.Fatal(err)
	}
	for name, tc := range map[string]struct{ token, issuer string }{
		"jwt":       {jwt, testIssuerName},
		"biscuit":   {sealed.Token, testIssuerName},
		"narrowed":  {narrowed, testIssuerName},
		"delegated": {delegated, auth.DelegationIssuerName(testIssuerName)},
	} {
		t.Run(name, func(t *testing.T) {
			v, err := f.verify(t, f.rotatedVerify, tc.token)
			if err != nil {
				t.Fatalf("a token of the previous key no longer verifies: %v", err)
			}
			if got := v.Capability().Issuer; got != tc.issuer {
				t.Errorf("issuer = %q, want %q", got, tc.issuer)
			}
		})
	}
}

// Without the previous key listed, the same token is refused: the test above
// passes because of it, not in spite of it.
func TestARotationWithoutThePreviousKeyRefusesItsTokens(t *testing.T) {
	f := newRotationFixture(t)
	jwt, err := issueWith(t, f.old.issuer)
	if err != nil {
		t.Fatal(err)
	}
	bare, err := newCapabilityIssuers(generateKey(t), nil, f.store, f.store, testIssuerName, nil, testCapTTL, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.verify(t, verifierOf(t, bare, f.store), jwt); !errors.Is(err, limes.ErrInvalidToken) {
		t.Fatalf("err = %v, want the unknown kid refused", err)
	}
}

// The previous key is published beside the signing key but never signs: what
// the rotated issuers mint verifies without it.
func TestAPreviousKeyIsPublishedNotUsed(t *testing.T) {
	f := newRotationFixture(t)
	oldKID := jwks.KeyID(f.oldKey.Public())
	if len(f.rotated.published) != 2 || !f.rotated.published[oldKID].Equal(f.oldKey.Public()) {
		t.Fatalf("published %v, want the signing key and the previous one under %q", f.rotated.published, oldKID)
	}
	for _, issuer := range []*limes.Issuer{f.rotated.issuer, f.rotated.delegation} {
		token, err := issueWith(t, issuer)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.verify(t, verifierOf(t, f.old, f.store), token); err == nil {
			t.Fatal("a token of the rotated issuers verifies under the previous key alone")
		}
	}
}

// Two keys under one kid, or one key under two kids, is refused: which key a
// token names would turn on the order of the configuration.
func TestCapabilityKeySetRefusesAmbiguity(t *testing.T) {
	store, err := memstore.NewStore(memstore.StoreConfig{})
	if err != nil {
		t.Fatal(err)
	}
	signing := generateKey(t)
	other := generateKey(t)
	for name, previous := range map[string][]limes.PublicKey{
		"the signing key again": {{KeyID: namedPreviousKID, Key: signing.Public()}},
		"the signing key's kid": {{KeyID: signing.KeyID, Key: other.Public()}},
		"a previous key twice":  {{KeyID: jwks.KeyID(other.Public()), Key: other.Public()}, {KeyID: namedPreviousKID, Key: other.Public()}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := newCapabilityIssuers(signing, previous, store, store, testIssuerName, nil, testCapTTL, zap.NewNop()); err == nil {
				t.Fatal("accepted")
			}
		})
	}
}
