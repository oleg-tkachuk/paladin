package auth

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/dpop"
	"github.com/oleg-tkachuk/limes/jwks"
	"github.com/oleg-tkachuk/limes/memstore"
	"github.com/oleg-tkachuk/limes/meter"
	"github.com/oleg-tkachuk/paladin/sdk/go/paladin"
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
	// pythonFrozen is the seed the Python SDK attenuated to ceilings of 0.
	pythonFrozen = filepath.Join("biscuit", "python_frozen.biscuit")
	biscuitCase  = filepath.Join("biscuit", "attenuation.json")
	biscuitSeed  = filepath.Join("biscuit", "seed.biscuit")
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
	// crossLangProofURL is the request the bound copy's proof is made for.
	crossLangProofURL = "https://paladin.example/paladin.data.v1.ObjectService/GetObject"
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
	Attenuate struct {
		Ops              []paladin.Op    `json:"ops"`
		ResourcePrefixes []string        `json:"resource_prefixes"`
		Planes           []paladin.Plane `json:"planes"`
		ExpiresAt        time.Time       `json:"expires_at"`
		BindJKT          string          `json:"bind_jkt"`
		MaxRequests      int64           `json:"max_requests"`
		MaxBudgetNanos   int64           `json:"max_budget_nanos"`
		// BindKeySeed is the hex seed of the Ed25519 key the copy is bound
		// to, so the proof of possession can be signed.
		BindKeySeed string `json:"bind_key_seed"`
	} `json:"attenuate"`
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

// crossLangVerifier verifies what the golden key signed, as of the golden
// clock, metering each Biscuit copy's own limits.
func crossLangVerifier(t *testing.T) *limes.Verifier {
	t.Helper()
	pub := ed25519.NewKeyFromSeed(crossLangSeed[:]).Public().(ed25519.PublicKey)
	keys, err := jwks.NewStatic(jwks.StaticConfig{Keys: map[string]ed25519.PublicKey{crossLangKID: pub}})
	if err != nil {
		t.Fatalf("keys: %v", err)
	}
	replay, err := dpop.NewMemoryReplayCache(dpop.MemoryReplayCacheConfig{})
	if err != nil {
		t.Fatalf("replay cache: %v", err)
	}
	proofs, err := dpop.NewVerifier(dpop.VerifierConfig{Replay: replay, Now: crossLangClock})
	if err != nil {
		t.Fatalf("proof verifier: %v", err)
	}
	v, err := limes.NewVerifier(limes.VerifierConfig{
		Keys:           keys,
		Revocations:    noBiscuitRevocations{},
		Biscuit:        &limes.BiscuitConfig{Revocations: noBiscuitRevocations{}, MeterCopies: true},
		TrustedIssuers: []string{crossLangIssuer},
		Now:            crossLangClock,
		DPoP:           proofs,
	})
	if err != nil {
		t.Fatalf("verifier: %v", err)
	}
	return v
}

// readCrossLangCase is the shared attenuation case.
func readCrossLangCase(t *testing.T) crossLangCase {
	t.Helper()
	var c crossLangCase
	if err := json.Unmarshal([]byte(readShared(t, biscuitCase)), &c); err != nil {
		t.Fatalf("parse case: %v", err)
	}
	return c
}

// The token the Python SDK attenuated verifies under limes, narrowed exactly
// as the shared case says.
func TestPythonAttenuationVerifies(t *testing.T) {
	c := readCrossLangCase(t)
	checkCrossLangAttenuation(t, c, readShared(t, pythonBiscuit), "Python")
}

// The Go SDK, which its clients narrow a Biscuit with, attenuates the seed
// into a token the verifier reads as the shared case says.
func TestGoSDKAttenuationVerifies(t *testing.T) {
	c := readCrossLangCase(t)
	a := c.Attenuate
	token, err := paladin.Attenuate(readShared(t, biscuitSeed), paladin.Attenuation{
		Ops: a.Ops, ResourcePrefixes: a.ResourcePrefixes, Planes: a.Planes, ExpiresAt: a.ExpiresAt,
		BindJKT: a.BindJKT, MaxRequests: &a.MaxRequests, MaxBudgetNanos: &a.MaxBudgetNanos,
	})
	if err != nil {
		t.Fatalf("the Go SDK refuses the shared case: %v", err)
	}
	checkCrossLangAttenuation(t, c, token, "Go SDK")
}

// checkCrossLangAttenuation verifies token, the seed attenuated by who with
// the shared case's arguments, and holds it to what the case expects.
func checkCrossLangAttenuation(t *testing.T, c crossLangCase, token, who string) {
	t.Helper()
	v := crossLangVerifier(t)
	bindSeed, err := hex.DecodeString(c.Attenuate.BindKeySeed)
	if err != nil || len(bindSeed) != ed25519.SeedSize {
		t.Fatalf("bind_key_seed %q is not %d hex bytes: %v", c.Attenuate.BindKeySeed, ed25519.SeedSize, err)
	}
	bindKey := ed25519.NewKeyFromSeed(bindSeed)
	ctx := context.Background()

	seedVerified, err := v.Verify(ctx, limes.VerifyRequest{Token: readShared(t, biscuitSeed), Audience: TokenPlaneMCP})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	seed := seedVerified.Capability()
	// The copy is bound, so it is accepted only with a proof from its key.
	proof, err := dpop.Sign(ctx, bindKey, dpop.SignRequest{
		Method: http.MethodPost, URL: crossLangProofURL, Token: token, IssuedAt: crossLangClock(),
	})
	if err != nil {
		t.Fatalf("proof: %v", err)
	}
	verified, err := v.Verify(ctx, limes.VerifyRequest{
		Token: token, Audience: TokenPlaneData,
		DPoP: dpop.Proof{JWT: proof, Method: http.MethodPost, URL: crossLangProofURL},
	})
	if err != nil {
		t.Fatalf("the %s-attenuated token is refused: %v", who, err)
	}
	got := verified.Capability()

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
	if len(verified.Copies()) != 1 || verified.Copies()[0].Requests == nil || verified.Copies()[0].Budget == nil ||
		*verified.Copies()[0].Requests != (limes.RequestCeiling{Max: want.CopyMaxRequests}) ||
		*verified.Copies()[0].Budget != (limes.BudgetCeiling{Max: limes.Nanos(want.CopyMaxBudget), UnitCode: seed.Caveats.Budget.UnitCode}) {
		t.Errorf("Copies = %+v, want one with %d requests and %d nanos", verified.Copies(), want.CopyMaxRequests, want.CopyMaxBudget)
	}
	if _, err := v.Verify(ctx, limes.VerifyRequest{Token: token, Audience: TokenPlaneMCP}); err == nil {
		t.Error("the plane the attenuation dropped is still accepted")
	}
}

// A copy the Python SDK froze — ceilings of 0 requests and 0 spend — verifies,
// and its first request and its first charge are refused, while the seed it
// came from is still metered as before.
func TestPythonFrozenCopyIsRefused(t *testing.T) {
	ctx := context.Background()
	v := crossLangVerifier(t)
	seed, err := v.Verify(ctx, limes.VerifyRequest{Token: readShared(t, biscuitSeed), Audience: TokenPlaneData})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	frozen, err := v.Verify(ctx, limes.VerifyRequest{Token: readShared(t, pythonFrozen), Audience: TokenPlaneData})
	if err != nil {
		t.Fatalf("the frozen copy is refused at verification: %v", err)
	}
	copies := frozen.Copies()
	if len(copies) != 1 || copies[0].Requests == nil || copies[0].Budget == nil ||
		*copies[0].Requests != (limes.RequestCeiling{}) || *copies[0].Budget != (limes.BudgetCeiling{UnitCode: seed.Capability().Caveats.Budget.UnitCode}) {
		t.Fatalf("Copies = %+v, want one with ceilings of 0 requests and 0 nanos", copies)
	}

	records, err := memstore.NewStore(memstore.StoreConfig{Now: crossLangClock})
	if err != nil {
		t.Fatalf("records: %v", err)
	}
	if err := records.Insert(ctx, limes.InsertRequest{Capability: seed.Capability(), IssuedBy: seed.Capability().Subject}); err != nil {
		t.Fatalf("record the seed: %v", err)
	}
	usage, err := memstore.NewUsageStore(memstore.UsageStoreConfig{Records: records, Now: crossLangClock})
	if err != nil {
		t.Fatalf("usage: %v", err)
	}
	// The smallest charge there is: a ceiling of 0 refuses even that.
	const smallestCharge limes.Nanos = 1

	if _, err := usage.Bump(ctx, meter.BumpRequest{Subject: frozen.MeterSubject()}); !errors.Is(err, limes.ErrRequestLimitExceeded) {
		t.Errorf("first request on the frozen copy: err = %v, want ErrRequestLimitExceeded", err)
	}
	if _, err := usage.Charge(ctx, meter.ChargeRequest{Subject: frozen.MeterSubject(), Amount: smallestCharge}, nil); !errors.Is(err, limes.ErrBudgetExceeded) {
		t.Errorf("first charge on the frozen copy: err = %v, want ErrBudgetExceeded", err)
	}
	if _, err := usage.Bump(ctx, meter.BumpRequest{Subject: seed.MeterSubject()}); err != nil {
		t.Errorf("a request on the seed after its copy froze: %v", err)
	}
	if _, err := usage.Charge(ctx, meter.ChargeRequest{Subject: seed.MeterSubject(), Amount: smallestCharge}, nil); err != nil {
		t.Errorf("a charge on the seed after its copy froze: %v", err)
	}
}
