package config

import (
	"os"
	"path/filepath"
	"testing"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/capability"
)

// The auto-charge amount reads exactly: 0.35 is 350 000 000 nanos, with no
// float64 between the file and the count, and a negative one is refused.
func TestChargePerRequestAmountIsExact(t *testing.T) {
	load := func(amount string) (Config, error) {
		dir := t.TempDir()
		minPath, overlay := filepath.Join(dir, "min.yaml"), filepath.Join(dir, "charge.yaml")
		if err := os.WriteFile(minPath, []byte(minimalConfigYAML), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(overlay, []byte("capability:\n  charge_per_request_amount: "+amount+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return Load([]string{minPath, overlay}, zap.NewNop())
	}
	cfg, err := load("0.35")
	if err != nil {
		t.Fatal(err)
	}
	if got, want := cfg.Capability.ChargePerRequestAmount, capability.MustParseAmount("0.35"); got != want {
		t.Errorf("charge_per_request_amount = %s, want %s", got, want)
	}
	if _, err := load("-1"); err == nil {
		t.Error("a negative charge_per_request_amount loaded")
	}
}
