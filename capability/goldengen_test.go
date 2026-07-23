package capability

import (
	"context"
	"crypto/ed25519"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Scaffolding for T003: mints the golden-token fixture with the CURRENT
// (pre-extraction) code, so the post-extraction module can prove the wire
// format did not change (FR-006 / SC-004, research R-007).
//
// Run explicitly:
//
//	go test ./internal/capability/ -run TestGenerateGoldenToken -tags goldengen
//
// The fixture is checked in; this file is scaffolding and is removed once
// the fixture exists. Every input below is fixed so the module-side test can
// reconstruct the same key and clock.

// goldenSeed is the fixed Ed25519 seed. Must stay identical to the constant
// in the module's golden_test.go — that is the whole point of pinning it.
var goldenSeed = [32]byte{
	0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
	0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10,
	0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18,
	0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f, 0x20,
}

const (
	goldenKID      = "golden-kid"
	goldenIssuer   = "paladin-golden"
	goldenAudience = "paladin-data"
	goldenSubject  = "golden-agent"
	goldenTenant   = "11111111-1111-1111-1111-111111111111"
)

// goldenClock is the fixed issuance instant. The module-side verifier pins
// the same value so nbf/exp stay inside the window forever.
func goldenClock() time.Time {
	return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
}

// genStore is a no-op Store — issuance only needs Insert to succeed.
type genStore struct{}

func (genStore) Insert(context.Context, Capability) error { return nil }
func (genStore) Get(context.Context, uuid.UUID) (*Capability, error) {
	return nil, errors.New("not found")
}
func (genStore) IsRevoked(context.Context, uuid.UUID) (bool, error) { return false, nil }
func (genStore) Revoke(context.Context, RevokeArgs) error           { return nil }
func (genStore) PurgeExpired(context.Context, time.Duration) (int64, error) {
	return 0, nil
}
func (genStore) ListByPrincipal(context.Context, ListByPrincipalArgs) ([]Capability, string, error) {
	return nil, "", nil
}

func TestGenerateGoldenToken(t *testing.T) {
	if os.Getenv("PALADIN_WRITE_GOLDEN") != "1" {
		t.Skip("set PALADIN_WRITE_GOLDEN=1 to (re)generate the fixture")
	}

	priv := ed25519.NewKeyFromSeed(goldenSeed[:])
	signer, err := NewEd25519Signer(goldenKID, priv)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}

	issuer, err := NewIssuer(IssuerConfig{
		Signer:     signer,
		Store:      genStore{},
		IssuerName: goldenIssuer,
		DefaultTTL: time.Hour,
		Now:        goldenClock,
	})
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}

	_, token, err := issuer.Issue(context.Background(), IssueRequest{
		Subject: Principal{
			Type:     PrincipalAgent,
			TenantID: uuid.MustParse(goldenTenant),
			Subject:  goldenSubject,
			Agent:    &AgentPrincipal{AgentType: "golden", Model: "fixture"},
		},
		Audience: []string{goldenAudience},
		Caveats: Caveats{
			Ops:              []Op{OpGet},
			ResourcePrefixes: []string{"golden/"},
			MaxRequests:      10,
			MaxBudgetAmount:  1.5,
			UnitCode:         "USD",
		},
		TTL: time.Hour,
	})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	out := filepath.Join("testdata", "golden_token.jwt")
	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(out, []byte(token), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Logf("wrote %s (%d bytes)", out, len(token))
}
