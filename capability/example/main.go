// Command example is the quickstart walkthrough as a runnable program:
// issue → verify → delegate → revoke, against the in-memory reference store.
//
// It deliberately mentions no object storage, no database and no Paladin concept.
// If this program needs any of those to run, the extraction has failed its
// central promise (FR-019, and the acceptance target for SC-001).
//
//	go run ./example
package main

import (
	"context"
	"crypto/ed25519"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/capability"
	"github.com/oleg-tkachuk/paladin/capability/memstore"
)

func main() {
	ctx := context.Background()
	tenantID := uuid.New()

	// 1. Mint a signing key. `kid` identifies it so tokens stay verifiable
	//    across rotation.
	kid, pub, priv, err := capability.GenerateEd25519Keypair()
	must(err, "generate keypair")

	// 2. Wire an issuer and a verifier. Storage is yours — here, in memory.
	//    UsageStore is generic in the transaction handle; with no transactions
	//    of our own we instantiate it over struct{} and pass nil callbacks.
	records := memstore.New[struct{}]()
	usage := memstore.NewUsage[struct{}](records)

	signer, err := capability.NewEd25519Signer(kid, priv)
	must(err, "signer")

	issuer, err := capability.NewIssuer(capability.IssuerConfig{
		Signer: signer, Store: records, IssuerName: "example-issuer",
	})
	must(err, "issuer")

	verifier, err := capability.NewStandardVerifier(capability.VerifierConfig{
		Keys:           capability.NewStaticKeyResolver(map[string]ed25519.PublicKey{kid: pub}),
		Revocations:    records,
		TrustedIssuers: []string{"example-issuer"},
	})
	must(err, "verifier")

	// 3. Issue to an orchestrator agent. Everything the authority permits is
	//    described by the token itself — no role, no long-lived secret.
	parent, parentToken, err := issuer.Issue(ctx, capability.IssueRequest{
		// Who asked for it — distinct from who it authorises, and required.
		IssuedBy: capability.Principal{Type: capability.PrincipalUser, TenantID: tenantID, Subject: "operator@example.com"},
		Subject: capability.Principal{
			Type:     capability.PrincipalAgent,
			TenantID: tenantID,
			Subject:  "research-orchestrator",
			Agent: &capability.AgentPrincipal{
				AgentType: "example-agent", Model: "demo", RunID: uuid.New(),
			},
		},
		Audience: []string{"my-service"},
		TTL:      15 * time.Minute,
		Caveats: capability.Caveats{
			Ops:              []capability.Op{capability.OpGet, capability.OpShare},
			ResourcePrefixes: []string{"corpus/public/"},
			MaxRequests:      500,
			MaxBudgetAmount:  25.00,
			UnitCode:         "USD",
		},
	})
	must(err, "issue")
	fmt.Printf("issued   %s  (%d bytes)\n", parent.ID, len(parentToken))

	// 4. Verify on every call. Local — no round trip to the issuer.
	if _, err := verifier.Verify(ctx, parentToken, "my-service"); err != nil {
		log.Fatalf("verify: %v", err)
	}
	fmt.Println("verified ok")

	// A token minted for one audience must not verify for another.
	if _, err := verifier.Verify(ctx, parentToken, "another-service"); err == nil {
		log.Fatal("audience gate did not hold")
	}
	fmt.Println("verified rejected for the wrong audience")

	// 5. Delegate to a sub-agent — strictly narrower, without calling back to
	//    any admin API. An orchestrator can attenuate, never escalate.
	child, _, err := issuer.Delegate(ctx, capability.DelegateRequest{
		Parent: *parent,
		Subject: capability.Principal{
			Type: capability.PrincipalAgent, TenantID: tenantID, Subject: "sub-worker",
			Agent: &capability.AgentPrincipal{
				AgentType: "example-agent", ParentAgentID: parent.ID,
			},
		},
		Audience: []string{"my-service"},
		TTL:      2 * time.Minute,
		Caveats: capability.Caveats{
			Ops:              []capability.Op{capability.OpGet}, // dropped OpShare
			ResourcePrefixes: []string{"corpus/public/2026/"},   // narrowed
			MaxRequests:      50,
			MaxBudgetAmount:  2.00, // 25.00 → 2.00
			UnitCode:         "USD",
		},
	})
	must(err, "delegate")
	fmt.Printf("delegated %s (child of %s)\n", child.ID, parent.ID)

	// Widening is refused — this is the property that makes the model safe to
	// hand to an agent.
	_, _, err = issuer.Delegate(ctx, capability.DelegateRequest{
		Parent: *parent,
		Subject: capability.Principal{
			Type: capability.PrincipalAgent, TenantID: tenantID, Subject: "greedy-worker",
		},
		Audience: []string{"my-service"},
		TTL:      time.Minute,
		Caveats: capability.Caveats{
			Ops:             []capability.Op{capability.OpGet},
			MaxBudgetAmount: 999, // wider than the parent's 25.00
			UnitCode:        "USD",
		},
	})
	if !errors.Is(err, capability.ErrDelegationTooWide) {
		log.Fatalf("widening delegation should have been refused, got %v", err)
	}
	fmt.Println("widening refused with ErrDelegationTooWide")

	// 6. Charge against the budget. Two ceilings; a rejection by either leaves
	//    both counters untouched, so a retry is always safe.
	spent, err := usage.Charge(ctx, parent.ID, 0.35, parent.Caveats.MaxBudgetAmount,
		"USD", tenantID, "search", "research-orchestrator", nil)
	must(err, "charge")
	fmt.Printf("charged  0.35 USD, spent now %.2f\n", spent)

	_, err = usage.Charge(ctx, parent.ID, 999, parent.Caveats.MaxBudgetAmount,
		"USD", tenantID, "search", "research-orchestrator", nil)
	if !errors.Is(err, capability.ErrBudgetExceeded) {
		log.Fatalf("over-budget charge should have been refused, got %v", err)
	}
	fmt.Println("over-budget charge refused with ErrBudgetExceeded")

	// 7. Revoke, cascading to everything the orchestrator delegated.
	must(records.Revoke(ctx, capability.RevokeArgs{
		ID: parent.ID, Reason: "example finished", Actor: "operator", CascadeChildren: true,
	}), "revoke")

	childRevoked, err := records.IsRevoked(ctx, child.ID)
	must(err, "is-revoked")
	if !childRevoked {
		log.Fatal("cascade did not reach the delegated child")
	}
	fmt.Println("revoked  parent + cascaded to the child")

	if _, err := verifier.Verify(ctx, parentToken, "my-service"); !errors.Is(err, capability.ErrRevoked) {
		log.Fatalf("revoked token should fail with ErrRevoked, got %v", err)
	}
	fmt.Println("verified now rejects the revoked token")
}

func must(err error, what string) {
	if err != nil {
		log.Fatalf("%s: %v", what, err)
	}
}
