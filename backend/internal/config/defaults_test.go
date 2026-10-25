package config

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/limes/meter"
)

// Defaults is what Load fills in where a configuration sets nothing: read
// off the same schema, field for field.
func TestDefaultsAreWhatLoadFillsIn(t *testing.T) {
	d, err := Defaults()
	if err != nil {
		t.Fatal(err)
	}
	minPath := filepath.Join(t.TempDir(), "min.yaml")
	if err := os.WriteFile(minPath, []byte(minimalConfigYAML), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load([]string{minPath}, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	// Blocks the minimal configuration leaves to the schema entirely: what
	// Load gives them is the defaults, all of it.
	for name, pair := range map[string][2]any{
		"logger.level":                    {d.Logger.Level, loaded.Logger.Level},
		"logger.sampling":                 {d.Logger.Sampling, loaded.Logger.Sampling},
		"datastores.postgres.pool":        {d.Datastores.Postgres.Pool, loaded.Datastores.Postgres.Pool},
		"worker.jobs.capability":          {d.Worker.Jobs.Capability, loaded.Worker.Jobs.Capability},
		"capability.revocation_cache_ttl": {d.Capability.RevocationCacheTTL, loaded.Capability.RevocationCacheTTL},
	} {
		if !reflect.DeepEqual(pair[0], pair[1]) {
			t.Errorf("%s: defaults %+v, Load fills in %+v", name, pair[0], pair[1])
		}
	}
	if d.Worker.Jobs.Capability.ChargesOlderThan != meter.MinChargeRetention {
		t.Errorf("charges_older_than defaults to %s", d.Worker.Jobs.Capability.ChargesOlderThan)
	}
	// A field with no default is left at its zero value, and so is one whose
	// default is a field the deployment must set itself.
	if d.App.Name != "" || d.Logger.Fields.Service != "" {
		t.Errorf("app.name %q, logger.fields.service %q; want both empty", d.App.Name, d.Logger.Fields.Service)
	}
}
