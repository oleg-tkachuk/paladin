// Package pgmoney carries an amount between limes.Nanos and a Postgres
// numeric exactly, with no float64 in between: every money column and every
// sum of one goes through it.
package pgmoney

import (
	"fmt"
	"math/big"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oleg-tkachuk/limes"
)

// nanosExp is the exponent of a nano: an amount is its count of nanos
// times 10^nanosExp units.
const nanosExp = -9

// decimalBase is the base of a numeric's exponent.
const decimalBase = 10

// NumericFromNanos is n as a numeric of nine decimals: exact, with no
// float64 in between.
func NumericFromNanos(n limes.Nanos) pgtype.Numeric {
	return pgtype.Numeric{Int: big.NewInt(int64(n)), Exp: nanosExp, Valid: true}
}

// NanosFromNumeric reads a money column back to Nanos exactly. NULL reads as
// zero, as a counter with no row does. A value finer than a nano, beyond
// int64, NaN or infinite is an error: the columns' CHECKs keep all of them
// out, so one here is corruption, not rounding to do.
func NanosFromNumeric(v pgtype.Numeric) (limes.Nanos, error) {
	if !v.Valid {
		return 0, nil
	}
	if v.NaN || v.InfinityModifier != pgtype.Finite {
		return 0, fmt.Errorf("pgmoney: amount is not finite")
	}
	n := new(big.Int).Set(v.Int)
	if shift := int64(v.Exp) - nanosExp; shift >= 0 {
		n.Mul(n, new(big.Int).Exp(big.NewInt(decimalBase), big.NewInt(shift), nil))
	} else {
		div := new(big.Int).Exp(big.NewInt(decimalBase), big.NewInt(-shift), nil)
		if new(big.Int).Rem(n, div).Sign() != 0 {
			return 0, fmt.Errorf("pgmoney: amount %s is finer than a nano", v.Int)
		}
		n.Quo(n, div)
	}
	if !n.IsInt64() {
		return 0, fmt.Errorf("pgmoney: amount beyond int64 nanos")
	}
	return limes.Nanos(n.Int64()), nil
}
