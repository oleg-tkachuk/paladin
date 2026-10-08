package apiutil

import (
	"math"
	"testing"

	"connectrpc.com/connect/v2"
	"google.golang.org/genproto/googleapis/type/money"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"github.com/oleg-tkachuk/paladin/capability"
	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

func TestMoneyRoundTripIsExact(t *testing.T) {
	for _, n := range []capability.Nanos{0, 1, 350_000_000, capability.NanosPerUnit, 25*capability.NanosPerUnit + 1, capability.MaxNanos} {
		m := MoneyOf("EUR", n)
		got, unit, err := NanosOf("max_budget", m)
		if err != nil || got != n || unit != "EUR" {
			t.Errorf("%s → %v → %s %s, %v", n, m, got, unit, err)
		}
	}
	if m := MoneyOf("", capability.NanosPerUnit); m.GetCurrencyCode() != capability.DefaultUnitCode {
		t.Errorf("MoneyOf with no unit = %v, want %s", m, capability.DefaultUnitCode)
	}
	if n, unit, err := NanosOf("max_budget", nil); err != nil || n != 0 || unit != "" {
		t.Errorf("an absent field = %s %q, %v; want zero and no unit", n, unit, err)
	}
}

func TestNanosOfRefusesWhatIsNotAnAmount(t *testing.T) {
	maxUnits := int64(capability.MaxNanos / capability.NanosPerUnit)
	for name, m := range map[string]*money.Money{
		"negative units":    {CurrencyCode: "USD", Units: -1},
		"negative nanos":    {CurrencyCode: "USD", Nanos: -1},
		"nanos past a unit": {CurrencyCode: "USD", Nanos: capability.NanosPerUnit},
		"past MaxNanos":     {CurrencyCode: "USD", Units: maxUnits + 1},
		"unknown currency":  {CurrencyCode: "BTC", Units: 1},
		"the old UNIT":      {CurrencyCode: "UNIT", Units: 1},
	} {
		if _, _, err := NanosOf("max_budget", m); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("%s: err = %v, want InvalidArgument", name, err)
		}
	}
}

// withUnknownDouble is msg as a client built against an older contract sends
// it: a double under field num, which this contract no longer declares.
func withUnknownDouble(t *testing.T, msg proto.Message, num protowire.Number) {
	t.Helper()
	raw, err := proto.Marshal(msg)
	if err != nil {
		t.Fatal(err)
	}
	raw = protowire.AppendTag(raw, num, protowire.Fixed64Type)
	raw = protowire.AppendFixed64(raw, math.Float64bits(25))
	if err := proto.Unmarshal(raw, msg); err != nil {
		t.Fatal(err)
	}
}

func TestRefuseRemovedFields(t *testing.T) {
	removedBudgets := []proto.Message{
		&adminv1.CapabilityCaveats{Ops: []string{"get"}},
		&adminv1.TenantBudgetServiceSetRequest{TenantId: "t", ResourceVersion: "1"},
	}
	for _, msg := range removedBudgets {
		t.Run(string(msg.ProtoReflect().Descriptor().Name()), func(t *testing.T) {
			if err := RefuseRemovedFields(msg); err != nil {
				t.Fatalf("a current request is refused: %v", err)
			}
			// Every removed number: the doubles and, after them, the micros.
			reserved := msg.ProtoReflect().Descriptor().ReservedRanges()
			for i := range reserved.Len() {
				for num := reserved.Get(i)[0]; num < reserved.Get(i)[1]; num++ {
					old := proto.Clone(msg)
					withUnknownDouble(t, old, num)
					if err := RefuseRemovedFields(old); connect.CodeOf(err) != connect.CodeInvalidArgument {
						t.Fatalf("removed field %d passed: err = %v", num, err)
					}
				}
			}
		})
	}

	// A field the server does not know but never removed is a newer client's,
	// and stays accepted, as unknown fields always have been.
	newer := &adminv1.CapabilityCaveats{Ops: []string{"get"}}
	withUnknownDouble(t, newer, protowire.MaxValidNumber)
	if err := RefuseRemovedFields(newer); err != nil {
		t.Fatalf("an unreserved unknown field is refused: %v", err)
	}
}
