// Package captest mints capabilities for tests the way the server does —
// issued, signed and verified — so a test holds a limes.VerifiedCapability,
// which only a verifier makes. Nothing outside tests imports it.
package captest

import (
	"context"
	"crypto/ed25519"
	"testing"
	"time"
	"uuid"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/jwks"
	"github.com/oleg-tkachuk/limes/memstore"
)

// Issuer is the `iss` the minted capabilities carry.
const Issuer = "captest"

// Audience is the audience a capability gets when its template names none,
// and the one it is verified for.
const Audience = "data"

// TTL is how long a minted capability lives.
const TTL = time.Hour

// Subject is the principal subject a capability gets when its template names
// none.
const Subject = "captest-agent"

// Unit is the currency a minted budget that names none is in: limes assumes
// no unit for an empty one.
const Unit = "USD"

// Minter issues and verifies capabilities over an in-memory store.
type Minter struct {
	Issuer   *limes.Issuer
	Verifier *limes.Verifier
	Store    *memstore.Store
}

// NewMinter builds a Minter with a fresh key and an empty store. Its
// verifier accepts Biscuits and admits copies with limits of their own.
func NewMinter(tb testing.TB) *Minter {
	tb.Helper()
	key, err := limes.GenerateKey()
	if err != nil {
		tb.Fatal(err)
	}
	signer, err := limes.NewSigner(limes.SignerConfig{KeyID: key.KeyID, Key: key.Private})
	if err != nil {
		tb.Fatal(err)
	}
	store, err := memstore.NewStore(memstore.StoreConfig{})
	if err != nil {
		tb.Fatal(err)
	}
	issuer, err := limes.NewIssuer(limes.IssuerConfig{Signer: signer, Store: store, IssuerName: Issuer, DefaultTTL: TTL})
	if err != nil {
		tb.Fatal(err)
	}
	keys, err := jwks.NewStatic(jwks.StaticConfig{Keys: map[string]ed25519.PublicKey{key.KeyID: key.Public()}})
	if err != nil {
		tb.Fatal(err)
	}
	verifier, err := limes.NewVerifier(limes.VerifierConfig{
		Keys: keys, Revocations: store, TrustedIssuers: []string{Issuer},
		Biscuit: &limes.BiscuitConfig{Revocations: store, MeterCopies: true},
	})
	if err != nil {
		tb.Fatal(err)
	}
	return &Minter{Issuer: issuer, Verifier: verifier, Store: store}
}

// Mint issues a capability shaped as template and verifies it, returning the
// verified capability and its token. The issuer assigns the id, the times and
// the issuer; what the template leaves out is filled in: a tenant, an agent
// subject, Audience, and the get operation. A ceiling the template leaves at
// its zero value is unlimited — a ceiling of nothing is set by asking for it
// with MintExact — and a budget that names no unit is in Unit. A key-bound
// template is refused: verifying one needs a proof of its key.
func (m *Minter) Mint(tb testing.TB, template limes.Capability) (limes.VerifiedCapability, string) {
	tb.Helper()
	c := template
	if c.Caveats.Requests == (limes.RequestCeiling{}) {
		c.Caveats.Requests = limes.RequestCeiling{Unlimited: true}
	}
	if c.Caveats.Budget.Max == 0 && !c.Caveats.Budget.Unlimited {
		c.Caveats.Budget.Unlimited = true
	}
	if c.Caveats.Budget.UnitCode == "" {
		c.Caveats.Budget.UnitCode = Unit
	}
	if c.Caveats.Copies == (limes.CopyQuota{}) {
		c.Caveats.Copies = limes.CopyQuota{Unlimited: true}
	}
	return m.MintExact(tb, c)
}

// MintExact is Mint with the template's ceilings as they are, zero included.
func (m *Minter) MintExact(tb testing.TB, template limes.Capability) (limes.VerifiedCapability, string) {
	tb.Helper()
	if template.ConfirmationJKT != "" {
		tb.Fatal("captest: a key-bound capability verifies only with a proof of its key")
	}
	subject := template.Subject
	if subject.TenantID == uuid.Nil() {
		subject.TenantID = uuid.New()
	}
	if subject.Type == limes.PrincipalTypeUnspecified {
		subject.Type = limes.PrincipalTypeAgent
	}
	if subject.Subject == "" {
		subject.Subject = Subject
	}
	audience := template.Audience
	if len(audience) == 0 {
		audience = []string{Audience}
	}
	caveats := template.Caveats
	if len(caveats.Ops) == 0 {
		caveats.Ops = []limes.Op{limes.OpGet}
	}
	res, err := m.Issuer.Issue(context.Background(), limes.IssueRequest{
		Subject:  subject,
		IssuedBy: limes.Principal{Type: limes.PrincipalTypeUser, TenantID: subject.TenantID, Subject: Subject},
		Audience: audience,
		Caveats:  caveats,
		TTL:      TTL,
	})
	if err != nil {
		tb.Fatalf("captest: issue: %v", err)
	}
	return m.Verify(tb, res.Token, audience[0]), res.Token
}

// Verify verifies token for audience.
func (m *Minter) Verify(tb testing.TB, token, audience string) limes.VerifiedCapability {
	tb.Helper()
	v, err := m.Verifier.Verify(context.Background(), limes.VerifyRequest{Token: token, Audience: audience})
	if err != nil {
		tb.Fatalf("captest: verify: %v", err)
	}
	return v
}

// Delegate delegates from parent with caveats, as a holder of parent would,
// and verifies the child.
func (m *Minter) Delegate(tb testing.TB, parent limes.VerifiedCapability, caveats limes.Caveats) (limes.VerifiedCapability, string) {
	tb.Helper()
	p := parent.Capability()
	res, err := m.Issuer.Delegate(context.Background(), limes.DelegateRequest{
		Parent: parent, Subject: p.Subject, Audience: p.Audience, Caveats: caveats,
	})
	if err != nil {
		tb.Fatalf("captest: delegate: %v", err)
	}
	return m.Verify(tb, res.Token, p.Audience[0]), res.Token
}

// Attenuated seals v as a Biscuit, narrows it by a and verifies the copy. v
// must allow limes.OpShare: limes seals no other.
func (m *Minter) Attenuated(tb testing.TB, v limes.VerifiedCapability, a limes.Attenuation) (limes.VerifiedCapability, string) {
	tb.Helper()
	b, err := m.Issuer.Biscuit(context.Background(), limes.BiscuitRequest{Capability: v})
	if err != nil {
		tb.Fatalf("captest: biscuit: %v", err)
	}
	token, err := limes.Attenuate(b.Token, a)
	if err != nil {
		tb.Fatalf("captest: attenuate: %v", err)
	}
	return m.Verify(tb, token, v.Capability().Audience[0]), token
}

// Verified mints a capability shaped as template on a Minter of its own; see
// Minter.Mint.
func Verified(tb testing.TB, template limes.Capability) limes.VerifiedCapability {
	tb.Helper()
	v, _ := NewMinter(tb).Mint(tb, template)
	return v
}
