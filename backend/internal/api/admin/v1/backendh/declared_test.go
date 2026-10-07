package backendh

import (
	"testing"
	"time"
)

// A bucket can be created only on a backend declared in storage.backends, and
// nothing said which those were: the console offered a backend registered
// through the API alone first, and the server refused the bucket. Every read
// of a backend now says whether it is declared.
func TestBackendsSayWhetherTheyAreDeclared(t *testing.T) {
	ctx := ctxWithRoles(rolePlatformAdmin)
	h := NewHandler(&pageRepo{}, allowAuthorizer{})
	h.SetDeclaredBackends([]string{"primary"})

	list, _, err := h.ListBackends(ctx, 0, "", "")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, b := range list {
		got[b.BackendID] = b.Declared
	}
	if !got["primary"] || got["secondary"] {
		t.Fatalf("declared = %v, want only primary", got)
	}

	one, err := h.GetBackend(ctx, "primary")
	if err != nil {
		t.Fatal(err)
	}
	if !one.Declared {
		t.Fatal("GetBackend: primary not declared")
	}

	// RotateCredentials answers from the read inside its transaction.
	rotated, err := h.RotateCredentials(ctx, "primary", "vault://kv/paladin/primary-2", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if !rotated.Declared {
		t.Fatal("RotateCredentials: primary not declared")
	}
}

func TestBackendsAreUndeclaredWithoutTheList(t *testing.T) {
	h := NewHandler(&pageRepo{}, allowAuthorizer{})
	h.SetDeclaredBackends(nil)
	b, err := h.GetBackend(ctxWithRoles(rolePlatformAdmin), "primary")
	if err != nil {
		t.Fatal(err)
	}
	if b.Declared {
		t.Fatal("declared with no storage.backends")
	}
}
