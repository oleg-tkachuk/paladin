package apiutil

import (
	"fmt"
	"math"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"github.com/oleg-tkachuk/paladin/capability"
)

// Money crosses the API as int64 micros (`*_micros`); the store still holds
// float64 amounts. These helpers are the only place the two meet.

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

// AmountFromMicros resolves a request's `<field>_micros` value; an absent
// optional field reads as 0, which every money field means as "unlimited".
func AmountFromMicros(field string, micros int64) (float64, error) {
	if micros < 0 || micros > capability.MaxMicros {
		return 0, connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("%s_micros: %d is outside 0..%d", field, micros, int64(capability.MaxMicros)))
	}
	return capability.MicrosToAmount(micros), nil
}

// RefuseRemovedFields refuses msg when it carries a field the contract has
// removed — a number in the message's reserved ranges, sent by a client built
// before the removal. Ignoring it would quietly change the request's meaning:
// a removed budget field read as absent is a budget lifted to unlimited.
//
// It sees only what the binary codec kept as unknown fields. Connect's JSON
// codec drops unknown names before a handler runs, so a JSON client sending a
// removed field cannot be told apart from one that sent nothing.
func RefuseRemovedFields(msg proto.Message) error {
	m := msg.ProtoReflect()
	reserved := m.Descriptor().ReservedRanges()
	for b := m.GetUnknown(); len(b) > 0; {
		num, typ, n := protowire.ConsumeTag(b)
		if n < 0 {
			return nil
		}
		if reserved.Has(num) {
			return connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("%s: field %d was removed from the API and is refused rather than ignored; upgrade the client",
					m.Descriptor().FullName(), num))
		}
		v := protowire.ConsumeFieldValue(num, typ, b[n:])
		if v < 0 {
			return nil
		}
		b = b[n+v:]
	}
	return nil
}
