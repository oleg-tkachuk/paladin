package capability

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Scaffolding for T005: records the pre-extraction p99 of Verify so SC-009
// has a numeric reference. Run explicitly:
//
//	PALADIN_WRITE_LATENCY=1 go test ./internal/capability/ -run TestLatencyBaseline
//
// SC-009 bounds the post-extraction p99 at +5% of what this writes.

type revLookup struct{}

func (revLookup) IsRevoked(context.Context, uuid.UUID) (bool, error) { return false, nil }

func TestLatencyBaseline(t *testing.T) {
	if os.Getenv("PALADIN_WRITE_LATENCY") != "1" {
		t.Skip("set PALADIN_WRITE_LATENCY=1 to (re)record the baseline")
	}

	priv := ed25519.NewKeyFromSeed(goldenSeed[:])
	pub := priv.Public().(ed25519.PublicKey)

	signer, err := NewEd25519Signer(goldenKID, priv)
	if err != nil {
		t.Fatalf("signer: %v", err)
	}
	issuer, err := NewIssuer(IssuerConfig{
		Signer: signer, Store: genStore{}, IssuerName: goldenIssuer,
		DefaultTTL: time.Hour, Now: goldenClock,
	})
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}
	_, token, err := issuer.Issue(context.Background(), IssueRequest{
		Subject:  Principal{Type: PrincipalService, TenantID: uuid.MustParse(goldenTenant), Subject: "bench"},
		Audience: []string{goldenAudience},
		Caveats:  Caveats{Ops: []Op{OpGet}},
		TTL:      time.Hour,
	})
	if err != nil {
		t.Fatalf("issue: %v", err)
	}

	verifier, err := NewStandardVerifier(VerifierConfig{
		Keys:           NewStaticKeyResolver(map[string]ed25519.PublicKey{goldenKID: pub}),
		Revocations:    revLookup{},
		TrustedIssuers: []string{goldenIssuer},
		Now:            goldenClock,
	})
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}

	const iterations = 10_000
	ctx := context.Background()
	// Warm up so the first-call allocations do not skew the tail.
	for i := 0; i < 1_000; i++ {
		_, _ = verifier.Verify(ctx, token, goldenAudience)
	}

	samples := make([]time.Duration, 0, iterations)
	for i := 0; i < iterations; i++ {
		start := time.Now()
		if _, err := verifier.Verify(ctx, token, goldenAudience); err != nil {
			t.Fatalf("verify: %v", err)
		}
		samples = append(samples, time.Since(start))
	}
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })

	p50 := samples[len(samples)*50/100]
	p99 := samples[len(samples)*99/100]

	out := filepath.Join("..", "..", "..", "specs",
		"003-capability-module-extraction", "baseline-latency.txt")
	body := fmt.Sprintf(
		"# Pre-extraction Verify latency baseline (T005, SC-009)\n"+
			"# iterations: %d (plus 1000 warm-up)\n"+
			"p50: %v\np99: %v\n"+
			"# SC-009 bound: post-extraction p99 must be <= %v (+5%%)\n",
		iterations, p50, p99, time.Duration(float64(p99)*1.05))
	if err := os.WriteFile(out, []byte(body), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	t.Logf("p50=%v p99=%v → %s", p50, p99, out)
}
