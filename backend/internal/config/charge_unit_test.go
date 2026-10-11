package config

import (
	"strings"
	"testing"
)

// The charge unit is any ISO 4217 code: CUE checks its shape, which a code
// ISO does not define also has, so Validate checks the code itself.
func TestChargeUnitIsAnISO4217Code(t *testing.T) {
	cases := map[string]bool{
		"":    true, // the capability's own unit
		"USD": true,
		"JPY": true,
		"XXX": true, // no currency
		"XYZ": false,
	}
	for unit, ok := range cases {
		c := minimalValidConfig()
		c.Capability.ChargePerRequestUnit = unit
		err := c.Validate()
		if ok && err != nil {
			t.Errorf("%q refused: %v", unit, err)
		}
		if !ok && (err == nil || !strings.Contains(err.Error(), "charge_per_request_unit")) {
			t.Errorf("%q: err = %v, want charge_per_request_unit refused", unit, err)
		}
	}
}
