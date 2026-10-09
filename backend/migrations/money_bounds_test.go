package migrations

import (
	"regexp"
	"testing"

	"github.com/oleg-tkachuk/limes"
)

// moneyBoundsMigration bounds every money column; SQL cannot import the Go
// constant it mirrors, so this test holds the two together.
const moneyBoundsMigration = "055_money_in_nanos.sql"

// moneyCheck matches one money column's CHECK: its scale and its upper bound.
var moneyCheck = regexp.MustCompile(`CHECK \(scale\((\w+)\) <= (\d+) AND (\w+) <= ([0-9.]+)\)`)

// nanosDecimals is the scale a Nanos carries.
const nanosDecimals = "9"

// Every money column is bounded at limes.MaxNanos, nine decimals.
func TestMoneyColumnsAreBoundedAtMaxNanos(t *testing.T) {
	raw, err := FS.ReadFile(moneyBoundsMigration)
	if err != nil {
		t.Fatal(err)
	}
	checks := moneyCheck.FindAllStringSubmatch(string(raw), -1)
	const moneyColumns = 10
	if len(checks) != moneyColumns {
		t.Fatalf("%d money CHECKs, want %d", len(checks), moneyColumns)
	}
	for _, m := range checks {
		column, scale, bounded, bound := m[1], m[2], m[3], m[4]
		if column != bounded || scale != nanosDecimals || bound != limes.MaxNanos.String() {
			t.Errorf("%s: scale %s, bound %s on %s; want scale %s, bound %s", column, scale, bound, bounded, nanosDecimals, limes.MaxNanos)
		}
	}
}
