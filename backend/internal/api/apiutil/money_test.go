package apiutil

import (
	"math"
	"testing"

	"connectrpc.com/connect/v2"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	adminv1 "github.com/oleg-tkachuk/paladin/sdk/go/gen/paladin/admin/v1"
)

func TestAmountFromMicros(t *testing.T) {
	cases := map[string]struct {
		micros int64
		want   float64
		code   connect.Code
	}{
		"micros":                        {micros: 12_500_000, want: 12.5},
		"0 means unlimited":             {micros: 0, want: 0},
		"one micro":                     {micros: 1, want: 0.000001},
		"negative micros":               {micros: -1, code: connect.CodeInvalidArgument},
		"micros beyond the exact range": {micros: 1_000_000_000_000_000, code: connect.CodeInvalidArgument},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := AmountFromMicros("max_budget", tc.micros)
			if tc.code != 0 {
				if connect.CodeOf(err) != tc.code {
					t.Fatalf("err = %v, want %v", err, tc.code)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %v, %v; want %v", got, err, tc.want)
			}
		})
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

// firstReserved is a field number msg's contract has removed.
func firstReserved(t *testing.T, msg proto.Message) protowire.Number {
	t.Helper()
	r := msg.ProtoReflect().Descriptor().ReservedRanges()
	if r.Len() == 0 {
		t.Fatalf("%T reserves no field", msg)
	}
	return r.Get(0)[0]
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
			withUnknownDouble(t, msg, firstReserved(t, msg))
			if err := RefuseRemovedFields(msg); connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("a removed field passed: err = %v", err)
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

func TestMicros(t *testing.T) {
	tenth, fifth := 0.1, 0.2
	for amount, want := range map[float64]int64{0: 0, -1: 0, 25: 25_000_000, 19.99: 19_990_000, tenth + fifth: 300_000} {
		if got := Micros(amount); got != want {
			t.Errorf("Micros(%v) = %d, want %d", amount, got, want)
		}
	}
}
