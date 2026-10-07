package presignttl

import (
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect/v2"

	"github.com/oleg-tkachuk/paladin/backend/internal/config"
)

const (
	getTTL  = 5 * time.Minute
	putTTL  = 10 * time.Minute
	partTTL = 15 * time.Minute
	maxTTL  = time.Hour
)

func policy(t *testing.T) Policy {
	t.Helper()
	p, err := New(getTTL, putTTL, partTTL, maxTTL)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestResolve(t *testing.T) {
	p := policy(t)
	cases := []struct {
		name      string
		op        Op
		requested time.Duration
		want      time.Duration
		wantCode  connect.Code // zero → success
	}{
		{"absent get → get_ttl", OpGet, 0, getTTL, 0},
		{"absent put → put_ttl", OpPut, 0, putTTL, 0},
		{"absent part → part_ttl", OpPart, 0, partTTL, 0},
		{"within bounds passes through", OpGet, 30 * time.Minute, 30 * time.Minute, 0},
		{"exactly max passes", OpPut, maxTTL, maxTTL, 0},
		{"one nanosecond under max passes", OpPart, maxTTL - time.Nanosecond, maxTTL - time.Nanosecond, 0},
		{"above max is refused, not clamped", OpGet, maxTTL + time.Nanosecond, 0, connect.CodeInvalidArgument},
		{"negative is refused", OpPut, -time.Second, 0, connect.CodeInvalidArgument},
		{"unknown op is a wiring bug", Op(99), 0, 0, connect.CodeInternal},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := p.Resolve(tc.op, tc.requested)
			if tc.wantCode != 0 {
				if code := connect.CodeOf(err); code != tc.wantCode {
					t.Fatalf("code = %v (err %v), want %v", code, err, tc.wantCode)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected err: %v", err)
			}
			if got != tc.want {
				t.Fatalf("Resolve(%s, %v) = %v, want %v", tc.op, tc.requested, got, tc.want)
			}
		})
	}
}

// A handler wired without a policy must refuse to mint, not mint with no
// bound — the zero value used to mean "fall back to a hard-coded week".
func TestZeroPolicyRefuses(t *testing.T) {
	_, err := Policy{}.Resolve(OpGet, time.Minute)
	if connect.CodeOf(err) != connect.CodeInternal || !errors.Is(err, ErrNotConfigured) {
		t.Fatalf("zero policy: err = %v, want Internal wrapping ErrNotConfigured", err)
	}
}

func TestNewRejectsInvalidLifetimes(t *testing.T) {
	cases := []struct {
		name                string
		get, put, part, max time.Duration
	}{
		{"zero max", getTTL, putTTL, partTTL, 0},
		{"max beyond SigV4", getTTL, putTTL, partTTL, config.SigV4MaxPresignExpiry + time.Second},
		{"zero get", 0, putTTL, partTTL, maxTTL},
		{"negative put", getTTL, -time.Second, partTTL, maxTTL},
		{"part above max", getTTL, putTTL, maxTTL + time.Second, maxTTL},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.get, tc.put, tc.part, tc.max); err == nil {
				t.Fatal("New accepted an invalid policy")
			}
		})
	}
}

func TestFromConfigReadsEachOperation(t *testing.T) {
	p, err := FromConfig(config.Presign{GetTTL: getTTL, PutTTL: putTTL, PartTTL: partTTL, MaxTTL: maxTTL})
	if err != nil {
		t.Fatal(err)
	}
	for op, want := range map[Op]time.Duration{OpGet: getTTL, OpPut: putTTL, OpPart: partTTL} {
		if got := p.Default(op); got != want {
			t.Errorf("Default(%s) = %v, want %v", op, got, want)
		}
	}
	if p.Max() != maxTTL {
		t.Errorf("Max = %v, want %v", p.Max(), maxTTL)
	}
}

func TestMustFromConfigPanicsOnUnloadedConfig(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("MustFromConfig accepted a zero config")
		}
	}()
	MustFromConfig(config.Presign{})
}

// A bucket's max_presign_put_ttl / max_presign_get_ttl was stored by the
// admin API and read by nothing. It now narrows max_ttl for that bucket.
func TestResolveWithinBucketCeiling(t *testing.T) {
	p := policy(t)
	const ceiling = 7 * time.Minute // below put_ttl (10m), above get_ttl (5m)
	cases := []struct {
		name      string
		op        Op
		requested time.Duration
		ceiling   time.Duration
		want      time.Duration
		refused   bool
	}{
		{"no ceiling behaves like Resolve", OpPut, 0, 0, putTTL, false},
		{"a default above the ceiling shrinks to it", OpPut, 0, ceiling, ceiling, false},
		{"a default below the ceiling is kept", OpGet, 0, ceiling, getTTL, false},
		{"a request at the ceiling passes", OpPut, ceiling, ceiling, ceiling, false},
		{"a request above the ceiling is refused", OpPut, ceiling + time.Second, ceiling, 0, true},
		{"a ceiling above max_ttl leaves max_ttl in charge", OpGet, maxTTL + time.Second, 2 * maxTTL, 0, true},
		{"a negative ceiling sets none", OpPut, maxTTL, -time.Second, maxTTL, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := p.ResolveWithin(tc.op, tc.requested, tc.ceiling)
			if tc.refused {
				if connect.CodeOf(err) != connect.CodeInvalidArgument {
					t.Fatalf("err = %v, want InvalidArgument", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Fatalf("ResolveWithin = %v, want %v", got, tc.want)
			}
		})
	}
}
