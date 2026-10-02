package apiutil

import (
	"fmt"
	"math"

	"connectrpc.com/connect"

	"github.com/oleg-tkachuk/paladin/capability"
)

// Money crosses the API as int64 micros (`*_micros`) and, for one release
// more, as the deprecated double beside it. These two helpers are the only
// place the two meet.

// Micros is an amount for a `*_micros` response field. A response reports
// what the store holds, so this never refuses: amounts beyond
// capability.MaxMicros (no single stored value reaches it) still round to
// the nearest micro.
func Micros(amount float64) int64 {
	if math.IsNaN(amount) || amount <= 0 {
		return 0
	}
	return int64(math.Round(amount * capability.MicrosPerUnit))
}

// AmountFromRequest resolves a request's money field. micros, when set (the
// optional field is present — 0 is a real value there, "unlimited"), wins; a deprecated double sent with it must name the same amount, since a
// client sending two different numbers has a bug the server must not paper
// over by picking one. Without micros the double is used as before.
func AmountFromRequest(field string, amount float64, micros int64, set bool) (float64, error) {
	if !set {
		return amount, nil
	}
	if micros < 0 || micros > capability.MaxMicros {
		return 0, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("%s_micros: %d is outside 0..%d", field, micros, int64(capability.MaxMicros)))
	}
	if amount != 0 {
		if m, err := capability.AmountToMicros(amount); err != nil || m != micros {
			return 0, connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("%s_amount %v and %s_micros %d disagree; send only %s_micros", field, amount, field, micros, field))
		}
	}
	return capability.MicrosToAmount(micros), nil
}
