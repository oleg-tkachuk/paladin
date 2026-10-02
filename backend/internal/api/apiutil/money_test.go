package apiutil

import (
	"testing"

	"connectrpc.com/connect"
)

func ptr(v int64) *int64 { return &v }

func TestAmountFromRequest(t *testing.T) {
	cases := map[string]struct {
		amount float64
		micros *int64
		want   float64
		code   connect.Code
	}{
		"double only (old client)":      {amount: 12.5, want: 12.5},
		"micros only":                   {micros: ptr(12_500_000), want: 12.5},
		"micros 0 means unlimited":      {micros: ptr(0), want: 0},
		"micros wins over absent 0.0":   {amount: 0, micros: ptr(1), want: 0.000001},
		"both, agreeing":                {amount: 0.1, micros: ptr(100_000), want: 0.1},
		"both, disagreeing":             {amount: 0.1, micros: ptr(200_000), code: connect.CodeInvalidArgument},
		"negative micros":               {micros: ptr(-1), code: connect.CodeInvalidArgument},
		"micros beyond the exact range": {micros: ptr(1_000_000_000_000_000), code: connect.CodeInvalidArgument},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			var m int64
			if tc.micros != nil {
				m = *tc.micros
			}
			got, err := AmountFromRequest("max_budget", tc.amount, m, tc.micros != nil)
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

func TestMicros(t *testing.T) {
	tenth, fifth := 0.1, 0.2
	for amount, want := range map[float64]int64{0: 0, -1: 0, 25: 25_000_000, 19.99: 19_990_000, tenth + fifth: 300_000} {
		if got := Micros(amount); got != want {
			t.Errorf("Micros(%v) = %d, want %d", amount, got, want)
		}
	}
}
