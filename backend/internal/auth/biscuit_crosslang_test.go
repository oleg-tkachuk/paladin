package auth

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/limes"
)

// The cross-language Biscuit case. limes mints seed.biscuit from its golden
// key and owns the vocabulary and the case; the Python SDK attenuates the
// seed into python.biscuit (sdk/python/tests/test_biscuit.py), and the
// verifier the server runs must read that token as the case expects.

// The limes module, whose testdata the files below are copies of.
const limesModule = "github.com/oleg-tkachuk/limes"

// sharedTestdata is where the SDKs keep the cases they share with the server.
var sharedTestdata = filepath.Join("..", "..", "..", "sdk", "testdata")

// The files copied from limes's testdata, by their path under both roots, and
// the token only the Python SDK writes.
var (
	limesCopies = []string{
		"biscuit_vocabulary.json",
		filepath.Join("biscuit", "seed.biscuit"),
		filepath.Join("biscuit", "attenuation.json"),
	}
	pythonBiscuit = filepath.Join("biscuit", "python.biscuit")
	biscuitCase   = filepath.Join("biscuit", "attenuation.json")
	biscuitSeed   = filepath.Join("biscuit", "seed.biscuit")
)

// The golden key, issuer and clock limes mints seed.biscuit with
// (goldengen_test.go there, unexported). TestSharedBiscuitFixturesAreLimes
// fails first if limes regenerates the seed, so these cannot drift unnoticed.
var crossLangSeed = [ed25519.SeedSize]byte{
	0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07, 0x08,
	0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f, 0x10,
	0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17, 0x18,
	0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f, 0x20,
}

const (
	crossLangKID    = "golden-kid"
	crossLangIssuer = "limes-golden"
)

func crossLangClock() time.Time { return time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC) }

// limesDir is the directory of the limes version backend/go.mod requires.
func limesDir(t *testing.T) string {
	t.Helper()
	out, err := exec.CommandContext(t.Context(), "go", "list", "-m", "-f", "{{.Dir}}", limesModule).Output()
	if err != nil {
		t.Fatalf("go list -m %s: %v", limesModule, err)
	}
	dir := strings.TrimSpace(string(out))
	if dir == "" {
		t.Fatalf("go list -m %s printed no directory; is the module downloaded?", limesModule)
	}
	return dir
}

// The SDKs test against copies of limes's cases; a copy that differs from the
// limes the server runs is a test of something else.
func TestSharedBiscuitFixturesAreLimes(t *testing.T) {
	origin := filepath.Join(limesDir(t), "testdata")
	for _, name := range limesCopies {
		want, err := os.ReadFile(filepath.Join(origin, name))
		if err != nil {
			t.Fatalf("read limes's %s: %v", name, err)
		}
		got, err := os.ReadFile(filepath.Join(sharedTestdata, name))
		if err != nil {
			t.Fatalf("read the copy of %s: %v", name, err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("sdk/testdata/%s differs from limes's testdata/%s; copy it from %s", name, name, origin)
		}
	}
}

// noBiscuitRevocations revokes nothing.
type noBiscuitRevocations struct{}

func (noBiscuitRevocations) IsRevoked(context.Context, uuid.UUID) (bool, error) { return false, nil }

func (noBiscuitRevocations) IsBiscuitRevoked(context.Context, [][]byte) (bool, error) {
	return false, nil
}

type crossLangCase struct {
	Expect struct {
		Ops              []limes.Op `json:"ops"`
		ResourcePrefixes []string   `json:"resource_prefixes"`
		Planes           []string   `json:"planes"`
		ExpiresAt        time.Time  `json:"expires_at"`
		ConfirmationJKT  string     `json:"confirmation_jkt"`
		CopyMaxRequests  int64      `json:"copy_max_requests"`
		CopyMaxBudget    int64      `json:"copy_max_budget_nanos"`
	} `json:"expect"`
}

func readShared(t *testing.T, name string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(sharedTestdata, name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return strings.TrimSpace(string(raw))
}

// The token the Python SDK attenuated verifies under limes, narrowed exactly
// as the shared case says.
func TestPythonAttenuationVerifies(t *testing.T) {
	var c crossLangCase
	if err := json.Unmarshal([]byte(readShared(t, biscuitCase)), &c); err != nil {
		t.Fatalf("parse case: %v", err)
	}
	pub := ed25519.NewKeyFromSeed(crossLangSeed[:]).Public().(ed25519.PublicKey)
	v, err := limes.NewStandardVerifier(limes.VerifierConfig{
		Keys:               limes.NewStaticKeyResolver(map[string]ed25519.PublicKey{crossLangKID: pub}),
		Revocations:        noBiscuitRevocations{},
		BiscuitRevocations: noBiscuitRevocations{},
		TrustedIssuers:     []string{crossLangIssuer},
		Now:                crossLangClock,
		AcceptBiscuit:      true,
		MeterCopies:        true,
	})
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}
	ctx := context.Background()

	seed, err := v.Verify(ctx, readShared(t, biscuitSeed), limes.AudiencePlaneMCP)
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	token := readShared(t, pythonBiscuit)
	got, err := v.Verify(ctx, token, limes.AudiencePlaneData)
	if err != nil {
		t.Fatalf("the Python-attenuated token is refused: %v", err)
	}

	want := c.Expect
	if got.ID != seed.ID {
		t.Errorf("ID = %s, want the seed's %s", got.ID, seed.ID)
	}
	if !slices.Equal(got.Caveats.Ops, want.Ops) {
		t.Errorf("Ops = %v, want %v", got.Caveats.Ops, want.Ops)
	}
	if !slices.Equal(got.Caveats.ResourcePrefixes, want.ResourcePrefixes) {
		t.Errorf("ResourcePrefixes = %v, want %v", got.Caveats.ResourcePrefixes, want.ResourcePrefixes)
	}
	if !slices.Equal(got.Audience, want.Planes) {
		t.Errorf("Audience = %v, want %v", got.Audience, want.Planes)
	}
	if !got.ExpiresAt.Equal(want.ExpiresAt) {
		t.Errorf("ExpiresAt = %v, want %v", got.ExpiresAt, want.ExpiresAt)
	}
	if got.ConfirmationJKT != want.ConfirmationJKT {
		t.Errorf("ConfirmationJKT = %q, want %q", got.ConfirmationJKT, want.ConfirmationJKT)
	}
	if len(got.Copies) != 1 || got.Copies[0].MaxRequests != want.CopyMaxRequests ||
		got.Copies[0].MaxBudget != limes.Nanos(want.CopyMaxBudget) {
		t.Errorf("Copies = %+v, want one with %d requests and %d nanos", got.Copies, want.CopyMaxRequests, want.CopyMaxBudget)
	}
	if _, err := v.Verify(ctx, token, limes.AudiencePlaneMCP); err == nil {
		t.Error("the plane the attenuation dropped is still accepted")
	}
}
