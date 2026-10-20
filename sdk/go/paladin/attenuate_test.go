package paladin_test

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
)

// sharedSeed is the Biscuit the cross-language case attenuates
// (sdk/testdata/biscuit/seed.biscuit); the server's tests verify what this
// SDK makes of it.
const sharedSeed = "../../testdata/biscuit/seed.biscuit"

func TestAttenuate(t *testing.T) {
	raw, err := os.ReadFile(sharedSeed)
	if err != nil {
		t.Fatal(err)
	}
	seed := strings.TrimSpace(string(raw))
	narrowed, err := paladin.Attenuate(seed, paladin.Attenuation{Ops: []paladin.Op{paladin.OpGet}})
	if err != nil || narrowed == "" || narrowed == seed {
		t.Fatalf("attenuate the seed: %q, %v", narrowed, err)
	}
	// A token that is no Biscuit is the caller's mistake, said in this
	// package's terms.
	if _, err := paladin.Attenuate("not-a-biscuit", paladin.Attenuation{}); !errors.Is(err, paladin.ErrInvalidArgument) {
		t.Errorf("a JWT-shaped string: %v, want ErrInvalidArgument", err)
	}
}
