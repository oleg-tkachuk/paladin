package capability

import (
	"errors"
	"math"
	"math/rand/v2"
	"testing"
)

func TestMicrosRoundTripIsExact(t *testing.T) {
	for _, m := range []int64{0, 1, 99, 100_000, 1_500_000, 123_456_789, 99_999_999_999_999, MaxMicros} {
		got, err := AmountToMicros(MicrosToAmount(m))
		if err != nil || got != m {
			t.Fatalf("%d micros → %v → %d, %v", m, MicrosToAmount(m), got, err)
		}
	}
}

func TestMicrosRoundTripIsExactAcrossTheRange(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	for range 200_000 {
		m := r.Int64N(MaxMicros + 1)
		if got, err := AmountToMicros(MicrosToAmount(m)); err != nil || got != m {
			t.Fatalf("%d micros → %d, %v", m, got, err)
		}
	}
}

func TestAmountToMicrosRoundsDecimalsThatAreNotFloats(t *testing.T) {
	tenth, fifth := 0.1, 0.2 // variables: constants would fold 0.1+0.2 to exactly 0.3
	cases := []struct {
		amount float64
		want   int64
	}{{0.1, 100_000}, {0.3, 300_000}, {tenth + fifth, 300_000}, {19.99, 19_990_000}, {1e-6, 1}, {4e-7, 0}, {6e-7, 1}}
	for _, c := range cases {
		if got, err := AmountToMicros(c.amount); err != nil || got != c.want {
			t.Errorf("AmountToMicros(%v) = %d, %v; want %d", c.amount, got, err, c.want)
		}
	}
}

func TestAmountToMicrosRefusesWhatCannotBeCounted(t *testing.T) {
	for _, amount := range []float64{-0.01, math.NaN(), math.Inf(1), 1e9} {
		if _, err := AmountToMicros(amount); !errors.Is(err, ErrInvalidAmount) {
			t.Errorf("AmountToMicros(%v): err = %v, want ErrInvalidAmount", amount, err)
		}
	}
}
