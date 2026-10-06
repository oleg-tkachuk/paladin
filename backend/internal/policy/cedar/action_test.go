package cedar

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
)

// Every declared action resolves back to itself by name, and no two share a
// name — a duplicate would make two call sites one Cedar action, so a grant
// written for one would silently cover the other.
func TestLookupActionRoundTripsEveryDeclaredAction(t *testing.T) {
	seen := map[string]bool{}
	for _, a := range Actions() {
		if seen[a.String()] {
			t.Errorf("%q is declared twice", a)
		}
		seen[a.String()] = true

		got, ok := LookupAction(a.String())
		if !ok || got != a {
			t.Errorf("LookupAction(%q) = %v, %v; want the declared action", a, got, ok)
		}
	}
}

// The words the credential handlers used to send are not actions.
func TestLookupActionRefusesUndeclaredNames(t *testing.T) {
	for _, name := range []string{"", "issue", "list", "read", "api_token:create", "issuecapability"} {
		if a, ok := LookupAction(name); ok {
			t.Errorf("LookupAction(%q) = %v, want not found", name, a)
		}
	}
}

// The zero Action is a wiring bug, not a request to deny: the engine says so
// instead of answering Deny, which a caller would report as permission denied.
func TestEngineRefusesTheZeroAction(t *testing.T) {
	tenant := uuid.New()
	e := NewEngine(fakeStore{}, time.Minute)
	dec, err := e.IsAuthorized(context.Background(),
		&Principal{Subject: "root", TenantID: tenant, Roles: []string{rolePlatformAdmin}},
		Action{},
		&Resource{TenantID: tenant},
		RequestContext{},
	)
	if !errors.Is(err, ErrUndeclaredAction) {
		t.Fatalf("err = %v, want ErrUndeclaredAction", err)
	}
	if dec != DecisionDeny {
		t.Fatalf("decision = %v, want Deny alongside the error", dec)
	}
}

// Actions hands out a copy: a caller appending to or rewriting it cannot change
// what LookupAction or the schema gate see.
func TestActionsReturnsACopy(t *testing.T) {
	got := Actions()
	got[0] = Action{}
	if Actions()[0] == (Action{}) {
		t.Fatal("Actions() exposes the registry itself")
	}
}
