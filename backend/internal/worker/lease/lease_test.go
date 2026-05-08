package lease

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"
)

// TestNew_Validation covers the constructor's parameter checks. We
// deliberately don't dial Postgres in unit tests — the SQL paths are
// exercised by the integration suite that talks to a real instance.
func TestNew_Validation(t *testing.T) {
	t.Parallel()

	holder := uuid.New()
	logger := zap.NewNop()

	cases := map[string]struct {
		cfg          Config
		wantContains string // expected substring in error; "pool" means config validation passed
	}{
		"missing name": {
			cfg:          Config{HolderID: holder, Logger: logger},
			wantContains: "Name required",
		},
		"missing holder id": {
			cfg:          Config{Name: "x", Logger: logger},
			wantContains: "HolderID required",
		},
		"missing logger": {
			cfg:          Config{Name: "x", HolderID: holder},
			wantContains: "Logger required",
		},
		"renew >= TTL/2": {
			cfg: Config{
				Name:          "x",
				HolderID:      holder,
				Logger:        logger,
				TTL:           10 * time.Second,
				RenewInterval: 10 * time.Second,
			},
			wantContains: "RenewInterval",
		},
		"defaults populated reaches pool check": {
			cfg:          Config{Name: "x", HolderID: holder, Logger: logger},
			wantContains: "pool required",
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := New(nil, tc.cfg)
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantContains)
			}
			if !strings.Contains(err.Error(), tc.wantContains) {
				t.Fatalf("error %q does not contain %q", err.Error(), tc.wantContains)
			}
		})
	}
}
