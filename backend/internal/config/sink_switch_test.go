package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/sinkkind"
)

// Every kind has a switch, under the key the health page and the admin API
// name: a kind whose key the loader does not accept could not be turned off.
func TestEverySinkKindHasASwitch(t *testing.T) {
	known := collectKnownPaths(reflect.TypeOf(Config{}), "")
	for _, kind := range sinkkind.All {
		if _, ok := known[SinkSwitchKey(kind)]; !ok {
			t.Errorf("%s has no switch: %q is not a config key", kind, SinkSwitchKey(kind))
		}
	}
}

// Each kind reads its own switch, and an unknown kind is off.
func TestSinkSwitchReadsItsKind(t *testing.T) {
	for _, kind := range sinkkind.All {
		var s DispatcherSinks
		on := reflect.ValueOf(&s).Elem()
		for i := range on.NumField() {
			if on.Type().Field(i).Tag.Get("yaml") == kind {
				on.Field(i).Set(reflect.ValueOf(SinkSwitch{Enabled: true}))
			}
		}
		for _, other := range sinkkind.All {
			if got := s.Enabled(other); got != (other == kind) {
				t.Errorf("with only %s on, Enabled(%s) = %v", kind, other, got)
			}
		}
	}
	if (DispatcherSinks{HTTP: SinkSwitch{Enabled: true}}).Enabled("carrier-pigeon") {
		t.Error("an unknown kind reads on")
	}
}

// loadWith loads the minimal config with overlay on top.
func loadWith(t *testing.T, overlay string) Config {
	t.Helper()
	dir := t.TempDir()
	minPath, overlayPath := filepath.Join(dir, "min.yaml"), filepath.Join(dir, "overlay.yaml")
	if err := os.WriteFile(minPath, []byte(minimalConfigYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(overlayPath, []byte(overlay), 0o600); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load([]string{minPath, overlayPath}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	return cfg
}

// Absent from the file, every kind is on: switching one off is opt-in, and
// turning one off leaves the others on.
func TestSinkKindsAreOnUntilSwitchedOff(t *testing.T) {
	for _, kind := range sinkkind.All {
		if !loadWith(t, "{}\n").Dispatcher.Sinks.Enabled(kind) {
			t.Errorf("%s is off by default", kind)
		}
	}
	cfg := loadWith(t, "dispatcher:\n  sinks:\n    rabbitmq:\n      enabled: false\n")
	for _, kind := range sinkkind.All {
		if got := cfg.Dispatcher.Sinks.Enabled(kind); got != (kind != sinkkind.RabbitMQ) {
			t.Errorf("with rabbitmq off, Enabled(%s) = %v", kind, got)
		}
	}
}
