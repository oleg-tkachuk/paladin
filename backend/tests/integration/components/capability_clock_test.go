//go:build integration

package components

import (
	"sync"
	"time"
)

// fakeClock is a clock a contract suite moves: it stands still but for
// Advance, so a check at an expiry's exact instant sees that instant.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

// newFakeClock starts at the wall clock, to the microsecond Postgres keeps:
// rows the database stamps with now() stay comparable with it.
func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Now().UTC().Truncate(time.Microsecond)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}
