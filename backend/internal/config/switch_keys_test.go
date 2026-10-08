package config

import (
	"reflect"
	"testing"
)

// A switch key the health page names must be one the loader accepts, or the
// page sends an operator to a key that does not exist.
func TestSwitchKeysAreConfigKeys(t *testing.T) {
	known := collectKnownPaths(reflect.TypeOf(Config{}), "")
	for _, key := range []string{KeyReplicaEnabled, KeyCapabilityEnabled, KeyAPITokenEnabled} {
		if _, ok := known[key]; !ok {
			t.Errorf("%q is not a config key", key)
		}
	}
}
