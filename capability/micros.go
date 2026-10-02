package capability

import (
	"fmt"
	"math"
)

// MicrosPerUnit is how many micro-units make one unit of a currency or
// metering unit: 1.5 USD is 1_500_000 micros.
const MicrosPerUnit = 1_000_000

// MaxMicros is the largest amount, in micros, that converts to and from a
// float64 amount exactly: fifteen significant digits, just under a billion
// units. A float64 holds any fifteen-digit decimal, so the round trip
// MicrosToAmount → AmountToMicros never loses a micro below it (the bound
// for that is 2^51 micros; 2^53 is not enough, the division rounds too).
const MaxMicros = 999_999_999_999_999

// AmountToMicros converts an amount in units to an integer count of micros,
// rounding to the nearest micro. It refuses what ValidateAmount refuses and
// amounts beyond MaxMicros.
//
// The rounding is what makes the conversion exact for any amount written
// with at most six decimals: 0.1 is not a float64, but the nearest float64
// to it times a million rounds back to 100000.
func AmountToMicros(amount float64) (int64, error) {
	if err := ValidateAmount(amount); err != nil {
		return 0, err
	}
	m := math.Round(amount * MicrosPerUnit)
	if m > MaxMicros {
		return 0, fmt.Errorf("%w: %v exceeds %d micros", ErrInvalidAmount, amount, int64(MaxMicros))
	}
	return int64(m), nil
}

// MicrosToAmount converts micros to an amount in units: the nearest float64,
// which AmountToMicros turns back into the same micros for anything up to
// MaxMicros.
func MicrosToAmount(micros int64) float64 {
	return float64(micros) / MicrosPerUnit
}
