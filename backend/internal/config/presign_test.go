package config

import (
	"strings"
	"testing"
	"time"
)

func validPresign() Presign {
	return Presign{
		GetTTL: 15 * time.Minute, PutTTL: 15 * time.Minute, PartTTL: 15 * time.Minute,
		MaxTTL: SigV4MaxPresignExpiry,
	}
}

// validLimits is limits.* as the CUE defaults produce it.
func validLimits() Limits {
	return Limits{
		MaxObjectSizeBytes: 100_000_000, MaxMultipartSizeBytes: 1_000_000_000_000,
		MinPartSizeBytes: 5 << 20, MaxPartSizeBytes: 5 << 30, MaxParts: 10000,
		Presign: validPresign(),
	}
}

// limits.* loaded whatever it held and was enforced nowhere; now that it is
// enforced, a value the policy cannot plan with fails at load instead.
func TestLimitsValidateAtLoad(t *testing.T) {
	c := minimalValidConfig()
	if err := c.Validate(); err != nil {
		t.Fatalf("defaults: %v", err)
	}
	c.Limits.MinPartSizeBytes = 5_000_000 // "5MB": below S3's 5 MiB part
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "min_part_size") {
		t.Fatalf("err = %v, want one naming min_part_size", err)
	}
}

// limits.presign was accepted whatever it held: a max_ttl of thirty days
// loaded fine and produced URLs every object store refuses at use time, and a
// per-method ttl above max_ttl was silently clamped. Both now fail at load.
func TestPresignValidate(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*Presign)
		wantErr string // empty → valid
	}{
		{"defaults are valid", func(*Presign) {}, ""},
		{"max_ttl at the SigV4 ceiling", func(p *Presign) { p.MaxTTL = SigV4MaxPresignExpiry }, ""},
		{"max_ttl beyond SigV4", func(p *Presign) { p.MaxTTL = SigV4MaxPresignExpiry + time.Second }, "max_ttl"},
		{"max_ttl zero", func(p *Presign) { p.MaxTTL = 0 }, "max_ttl"},
		{"get_ttl above max_ttl", func(p *Presign) { p.MaxTTL = time.Hour; p.GetTTL = 2 * time.Hour }, "get_ttl"},
		{"put_ttl zero", func(p *Presign) { p.PutTTL = 0 }, "put_ttl"},
		{"part_ttl negative", func(p *Presign) { p.PartTTL = -time.Minute }, "part_ttl"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := validPresign()
			tc.mutate(&p)
			err := p.Validate()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected err: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want one naming %q", err, tc.wantErr)
			}
		})
	}
}
