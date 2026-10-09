package pgmoney

import (
	"math/big"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oleg-tkachuk/limes"
)

func TestNanosNumericRoundTripIsExact(t *testing.T) {
	for _, n := range []limes.Nanos{0, 1, 300_000_000, limes.NanosPerUnit, limes.MaxNanos} {
		got, err := NanosFromNumeric(NumericFromNanos(n))
		if err != nil || got != n {
			t.Errorf("%s → %s, %v", n, got, err)
		}
	}
}

// What Postgres hands back is a numeric of whatever scale the value was
// written or summed at; any of them reads to its nanos.
func TestNanosFromNumericReadsAnyScale(t *testing.T) {
	cases := []struct {
		v    pgtype.Numeric
		want limes.Nanos
	}{
		{pgtype.Numeric{Int: big.NewInt(15), Exp: -1, Valid: true}, 1_500_000_000},    // 1.5
		{pgtype.Numeric{Int: big.NewInt(350_000), Exp: -6, Valid: true}, 350_000_000}, // 0.350000, a numeric(14,6) row
		{pgtype.Numeric{Int: big.NewInt(25), Exp: 0, Valid: true}, 25_000_000_000},    // 25
		{pgtype.Numeric{Int: big.NewInt(2), Exp: 1, Valid: true}, 20_000_000_000},     // 2e1
		{pgtype.Numeric{Int: big.NewInt(1_000), Exp: -12, Valid: true}, 1},            // 0.000000001000
		{pgtype.Numeric{}, 0}, // NULL
	}
	for _, c := range cases {
		if got, err := NanosFromNumeric(c.v); err != nil || got != c.want {
			t.Errorf("%v × 10^%d → %s, %v; want %s", c.v.Int, c.v.Exp, got, err, c.want)
		}
	}
}

func TestNanosFromNumericRefusesWhatIsNotAnAmount(t *testing.T) {
	huge := new(big.Int).Lsh(big.NewInt(1), 70)
	for name, v := range map[string]pgtype.Numeric{
		"finer than a nano": {Int: big.NewInt(1), Exp: -10, Valid: true},
		"beyond int64":      {Int: huge, Exp: 0, Valid: true},
		"NaN":               {NaN: true, Valid: true},
		"infinite":          {InfinityModifier: pgtype.Infinity, Valid: true},
	} {
		if _, err := NanosFromNumeric(v); err == nil {
			t.Errorf("%s: read as an amount", name)
		}
	}
}
