package auth

import (
	"testing"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/meter"
)

// Every code limes and its meter define has a row in meterCodes, so a code a
// release adds fails here rather than answering as the store failing; and
// every row names a code they define, so a renamed one cannot linger.
func TestMeterCodesCoverEveryLimesCode(t *testing.T) {
	defined := map[limes.Code]bool{}
	for _, c := range append(limes.Codes(), meter.Codes()...) {
		defined[c] = true
		if _, ok := meterCodes[c]; !ok {
			t.Errorf("limes code %q has no row in meterCodes", c)
		}
	}
	for c := range meterCodes {
		if !defined[c] {
			t.Errorf("meterCodes names %q, which limes does not define", c)
		}
	}
}
