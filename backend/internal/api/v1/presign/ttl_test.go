package presign

import (
	"testing"
	"time"
)

func TestResolveTTLClamps(t *testing.T) {
	// Unset config → fallback ceiling/default applied by NewHandler.
	h := NewHandler(nil, nil, nil, Config{})
	if h.cfg.MaxTTL != fallbackMaxTTL {
		t.Errorf("MaxTTL: got %v want %v", h.cfg.MaxTTL, fallbackMaxTTL)
	}
	if h.cfg.DefaultTTL != fallbackDefaultTTL {
		t.Errorf("DefaultTTL: got %v want %v", h.cfg.DefaultTTL, fallbackDefaultTTL)
	}

	cases := []struct {
		name string
		in   time.Duration
		want time.Duration
	}{
		{"zero → default", 0, fallbackDefaultTTL},
		{"negative → default", -time.Second, fallbackDefaultTTL},
		{"within bound", time.Hour, time.Hour},
		{"10y over ceiling → max", 87600 * time.Hour, fallbackMaxTTL},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := h.resolveTTL(tc.in); got != tc.want {
				t.Errorf("resolveTTL(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestNewHandlerClampsDefaultAboveMax(t *testing.T) {
	h := NewHandler(nil, nil, nil, Config{DefaultTTL: 48 * time.Hour, MaxTTL: time.Hour})
	if h.cfg.DefaultTTL != time.Hour {
		t.Errorf("DefaultTTL should be clamped to MaxTTL, got %v", h.cfg.DefaultTTL)
	}
}
