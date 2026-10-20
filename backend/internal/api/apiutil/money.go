package apiutil

import (
	"errors"
	"fmt"
	"strings"

	"connectrpc.com/connect/v2"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"github.com/oleg-tkachuk/limes"
)

// Money crosses the API as google.type.Money and is counted as
// limes.Nanos; these helpers are the only place the two meet, and
// neither goes through a float64.

// NoUnit is the currency an amount counted in no unit crosses the API in:
// ISO 4217's XXX, "no currency involved". limes assumes no unit for an empty
// one, and google.type.Money has no spelling for none.
const NoUnit = limes.AbstractUnitCode

// ErrUnknownUnitCode is a currency code that is no ISO 4217 code.
var ErrUnknownUnitCode = errors.New("not an ISO 4217 currency code")

// NormalizeUnitCode is code in the canonical upper case limes counts in, or
// ErrUnknownUnitCode when it is no ISO 4217 code. An empty code is refused
// too: it names no unit, which is the caller's to decide.
func NormalizeUnitCode(code string) (string, error) {
	upper := strings.ToUpper(strings.TrimSpace(code))
	if !limes.IsUnitCode(upper) {
		return "", fmt.Errorf("unit_code %q: %w", code, ErrUnknownUnitCode)
	}
	return upper, nil
}

// unitOrNone is the first of units that names one, or NoUnit.
func unitOrNone(units ...string) string {
	for _, u := range units {
		if u != "" {
			return u
		}
	}
	return NoUnit
}

// MoneyOf is n of unit as a google.type.Money; an amount in no unit is in
// NoUnit.
func MoneyOf(unit string, n limes.Nanos) *money.Money {
	unit = unitOrNone(unit)
	return &money.Money{
		CurrencyCode: unit,
		Units:        int64(n / limes.NanosPerUnit),
		Nanos:        int32(n % limes.NanosPerUnit),
	}
}

// NanosOf resolves a request's Money field to its amount and its unit, a
// code NormalizeUnitCode accepts. An absent field is zero with an
// empty unit, and a field that names no currency keeps an empty unit too:
// the caller decides what an unnamed unit means — the platform's currency,
// or a parent's — rather than this deciding it. A negative amount,
// nanos outside 0..999999999, one past limes.MaxNanos or an unknown
// currency is InvalidArgument naming field.
func NanosOf(field string, m *money.Money) (limes.Nanos, string, error) {
	if m == nil {
		return 0, "", nil
	}
	invalid := func(format string, args ...any) (limes.Nanos, string, error) {
		return 0, "", connect.Errorf(connect.CodeInvalidArgument, "%s: "+format, append([]any{field}, args...)...)
	}
	unit := m.GetCurrencyCode()
	if unit != "" {
		normalized, err := NormalizeUnitCode(unit)
		if err != nil {
			return invalid("%w", err)
		}
		unit = normalized
	}
	units, nanos := m.GetUnits(), int64(m.GetNanos())
	if units < 0 || nanos < 0 || nanos >= limes.NanosPerUnit {
		return invalid("units %d and nanos %d are not a non-negative amount", units, nanos)
	}
	if units > int64(limes.MaxNanos/limes.NanosPerUnit) {
		return invalid("%d units exceed %s", units, limes.MaxNanos)
	}
	n := limes.Nanos(units*limes.NanosPerUnit + nanos)
	if err := n.Validate(); err != nil {
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
			return connect.Errorf(connect.CodeInvalidArgument, "%s: field %d was removed from the API and is refused rather than ignored; upgrade the client",
				m.Descriptor().FullName(), num)
		}
		v := protowire.ConsumeFieldValue(num, typ, b[n:])
		if v < 0 {
			return nil
		}
		b = b[n+v:]
	}
	return nil
}
