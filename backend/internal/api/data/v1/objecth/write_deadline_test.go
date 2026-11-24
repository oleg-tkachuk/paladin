package objecth

import (
	"errors"
	"testing"
	"time"
)

func TestCapWriteTTL(t *testing.T) {
	now := time.Now()
	const ttl = time.Hour
	cases := map[string]struct {
		deadline time.Time
		ok       bool
		want     time.Duration
		closed   bool
	}{
		"no deadline":        {time.Time{}, false, ttl, false},
		"a deadline after":   {now.Add(2 * ttl), true, ttl, false},
		"a deadline sooner":  {now.Add(time.Minute), true, time.Minute, false},
		"a deadline passed":  {now.Add(-time.Second), true, 0, true},
		"a deadline reached": {now, true, 0, true},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := CapWriteTTL(ttl, tc.deadline, tc.ok, now)
			if errors.Is(err, ErrWritesClosed) != tc.closed {
				t.Fatalf("err = %v, want closed %v", err, tc.closed)
			}
			if got != tc.want {
				t.Fatalf("ttl = %v, want %v", got, tc.want)
			}
		})
	}
}
