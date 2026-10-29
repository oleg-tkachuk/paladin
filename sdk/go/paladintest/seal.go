package paladintest

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"time"
	"uuid"

	"connectrpc.com/connect/v2"
	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/biscuit"
	"github.com/oleg-tkachuk/limes/jwks"
	"github.com/oleg-tkachuk/limes/memstore"

	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// A capability's Biscuit is a real one, sealed by the same library the server
// seals with, under a key of the fake's own: paladin.Attenuate narrows it, and
// a narrowed copy presented to the fake is verified by that library and held
// to its narrowing — operations, resources, planes and expiry — as the server
// holds it. The copy counts against the capability it came from. A copy's own
// request and budget limits are accepted and not counted, as the fake counts
// no budget.

// The terms the fake seals a capability under.
const (
	// sealUnit is the unit of the sealed capability's budget, which the fake
	// never charges.
	sealUnit = "USD"
	// sealedBy is who the sealed capability records as requesting it.
	sealedBy = "paladintest"
	// sealedClient is the OAuth client the sealed capability is granted to
	// (RFC 9068 client_id), which limes requires and nothing checks.
	sealedClient = "paladintest"
)

// planes are every plane a copy may be presented to, for a call whose plane
// no capability verifier stands in front of.
var planes = []paladin.Plane{paladin.PlaneData, paladin.PlaneAdmin, paladin.PlaneIAM, paladin.PlaneMCP}

// sealer issues and verifies the Biscuit form of the fake's capabilities.
type sealer struct {
	issuer   *limes.Issuer
	verifier *limes.Verifier
	// records is the fake's capability id for each capability sealed, by the
	// id the library gave it.
	records map[uuid.UUID]string
}

func newSealer() (*sealer, error) {
	key, err := limes.GenerateKey()
	if err != nil {
		return nil, err
	}
	signer, err := limes.NewSigner(limes.SignerConfig{KeyID: key.KeyID, Key: key.Private})
	if err != nil {
		return nil, err
	}
	store, err := memstore.NewStore(memstore.StoreConfig{})
	if err != nil {
		return nil, err
	}
	issuer, err := limes.NewIssuer(limes.IssuerConfig{Biscuit: biscuit.Form(), Signer: signer, Store: store, IssuerName: capabilityIssuer, DefaultTTL: DefaultCapabilityTTL})
	if err != nil {
		return nil, err
	}
	keys, err := jwks.NewStatic(jwks.StaticConfig{Keys: map[string]ed25519.PublicKey{key.KeyID: key.Public()}})
	if err != nil {
		return nil, err
	}
	verifier, err := limes.NewVerifier(limes.VerifierConfig{
		Keys: keys, Revocations: store, TrustedIssuers: []string{capabilityIssuer},
		Biscuit: &limes.BiscuitConfig{Form: biscuit.Form(), Revocations: store, MeterCopies: true},
	})
	if err != nil {
		return nil, err
	}
	return &sealer{issuer: issuer, verifier: verifier, records: map[uuid.UUID]string{}}, nil
}

// seal is the Biscuit of c, a capability the fake issued now and that expires
// at expires. The caller holds mu.
func (s *sealer) seal(c *adminv1.Capability, now, expires time.Time) (string, error) {
	ctx := context.Background()
	tenant, err := uuid.Parse(c.GetSubject().GetTenantId())
	if err != nil {
		return "", fmt.Errorf("subject tenant: %w", err)
	}
	subject := limes.Principal{Type: limes.PrincipalTypeService, TenantID: tenant, Subject: c.GetSubject().GetSubject()}
	res, err := s.issuer.Issue(ctx, limes.IssueRequest{
		Subject:  subject,
		IssuedBy: limes.Principal{Type: limes.PrincipalTypeService, TenantID: tenant, Subject: sealedBy},
		Audience: c.GetAudience(),
		Caveats:  sealedCaveats(c.GetCaveats()),
		TTL:      sealedTTL(expires.Sub(now)),
		ClientID: sealedClient,
	})
	if err != nil {
		return "", err
	}
	if len(c.GetAudience()) == 0 {
		return "", fmt.Errorf("capability %s names no plane", c.GetId())
	}
	vc, err := s.verifier.Verify(ctx, limes.VerifyRequest{Token: res.Token, Audience: c.GetAudience()[0]})
	if err != nil {
		return "", err
	}
	b, err := s.issuer.Biscuit(ctx, limes.BiscuitRequest{Capability: vc})
	if err != nil {
		return "", err
	}
	s.records[res.Capability.ID] = c.GetId()
	return b.Token, nil
}

// sealedTTL is ttl as the library takes one: whole seconds, rounded up so
// the Biscuit outlives the capability rather than stopping before it.
func sealedTTL(ttl time.Duration) time.Duration {
	return ttl.Truncate(time.Second) + time.Second
}

// sealedCaveats is caveats as the library seals them: what a call is checked
// against, and no limit the fake counts itself.
func sealedCaveats(caveats *adminv1.CapabilityCaveats) limes.Caveats {
	c := limesCaveats(caveats)
	c.Requests = limes.RequestCeiling{Unlimited: true}
	c.Budget = limes.BudgetCeiling{Unlimited: true, UnitCode: sealUnit}
	c.Copies = limes.CopyQuota{Unlimited: true}
	return c
}

// open verifies token, a capability the fake did not issue as it stands — a
// narrowed copy of a Biscuit, or none at all — for plane, or for any plane
// when plane is "", and returns the id of the fake's capability it came from
// and the capability as its blocks narrow it. A refusal is the library's, as
// the server gives it. The caller holds mu.
func (s *sealer) open(token string, plane paladin.Plane) (string, limes.Capability, error) {
	candidates := planes
	if plane != "" {
		candidates = []paladin.Plane{plane}
	}
	var err error
	for _, p := range candidates {
		var vc limes.VerifiedCapability
		vc, err = s.verifier.Verify(context.Background(), limes.VerifyRequest{Token: token, Audience: string(p)})
		if err == nil {
			c := vc.Capability()
			return s.records[c.ID], c, nil
		}
	}
	return "", limes.Capability{}, capabilityRefusal(connect.CodePermissionDenied, err)
}

// biscuitOfLocked is the credential of capability id's Biscuit, which a copy
// of it stands for: its tenant, and its revocation. The caller holds mu.
func (s *Server) biscuitOfLocked(id string) (*credential, bool) {
	rec, ok := s.capabilities[id]
	if !ok {
		return nil, false
	}
	for _, token := range rec.credentials {
		if c := s.credentials[token]; c.biscuit {
			return c, true
		}
	}
	return nil, false
}

// opsOf is ops as this module names them.
func opsOf(ops []limes.Op) []paladin.Op {
	out := make([]paladin.Op, len(ops))
	for i, op := range ops {
		out[i] = paladin.Op(op)
	}
	return out
}
