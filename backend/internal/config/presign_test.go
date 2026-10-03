package config

import (
	"strings"
	"testing"
	"time"
)

func validPresign() Presign {
	return Presign{
		GetTTL: 15 * time.Minute, PutTTL: 15 * time.Minute, PartTTL: 15 * time.Minute,
		MaxTTL: SigV4MaxPresignExpiry, DefaultMaxSize: 1 << 30,
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
		{"default_max_size zero", func(p *Presign) { p.DefaultMaxSize = 0 }, "default_max_size"},
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
