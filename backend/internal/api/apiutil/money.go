package apiutil

import (
	"fmt"

	"connectrpc.com/connect"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"github.com/oleg-tkachuk/paladin/capability"
)

// Money crosses the API as google.type.Money and is counted as
// capability.Nanos; these helpers are the only place the two meet, and
// neither goes through a float64.

// MoneyOf is n of unit as a google.type.Money; unit "" is
// capability.DefaultUnitCode, as everywhere a unit is left out.
func MoneyOf(unit string, n capability.Nanos) *money.Money {
	if unit == "" {
		unit = capability.DefaultUnitCode
	}
	return &money.Money{
		CurrencyCode: unit,
		Units:        int64(n / capability.NanosPerUnit),
		Nanos:        int32(n % capability.NanosPerUnit), //nolint:gosec // the remainder of a division by 10^9 fits int32
	}
}

// NanosOf resolves a request's Money field to its amount and its unit, a
// code capability.NormaliseUnitCode accepts. An absent field is zero with an
// empty unit, so the caller decides what absent means. A negative amount,
// nanos outside 0..999999999, one past capability.MaxNanos or an unknown
// currency is InvalidArgument naming field.
func NanosOf(field string, m *money.Money) (capability.Nanos, string, error) {
	if m == nil {
		return 0, "", nil
	}
	invalid := func(format string, args ...any) (capability.Nanos, string, error) {
		return 0, "", connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("%s: "+format, append([]any{field}, args...)...))
	}
	unit, err := capability.NormaliseUnitCode(m.GetCurrencyCode())
	if err != nil {
		return invalid("%w", err)
	}
	units, nanos := m.GetUnits(), int64(m.GetNanos())
	if units < 0 || nanos < 0 || nanos >= capability.NanosPerUnit {
		return invalid("units %d and nanos %d are not a non-negative amount", units, nanos)
	}
	if units > int64(capability.MaxNanos/capability.NanosPerUnit) {
		return invalid("%d units exceed %s", units, capability.MaxNanos)
	}
	n := capability.Nanos(units*capability.NanosPerUnit + nanos)
	if err := capability.ValidateAmount(n); err != nil {
		return invalid("%w", err)
	}
	return n, unit, nil
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
