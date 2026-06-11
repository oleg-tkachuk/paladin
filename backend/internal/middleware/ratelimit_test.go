package middleware

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"golang.org/x/time/rate"
)

func TestTenantRateLimiter_GetLimiter(t *testing.T) {
	rl := NewTenantRateLimiter(rate.Limit(10), 20, 100, time.Minute, time.Minute)

	l1 := rl.GetLimiter("tenant1")
	assert.NotNil(t, l1)
	assert.InDelta(t, float64(rate.Limit(10)), float64(l1.Limit()), 0.0001)
	assert.Equal(t, 20, l1.Burst())

	l2 := rl.GetLimiter("tenant1")
	assert.Same(t, l1, l2, "should return same limiter for same tenant")

	l3 := rl.GetLimiter("tenant2")
	assert.NotSame(t, l1, l3)
}

func TestTenantRateLimiter_Eviction(t *testing.T) {
	// Max 2 entries
	rl := NewTenantRateLimiter(rate.Limit(10), 20, 2, time.Minute, time.Minute)

	rl.GetLimiter("tenant1")
	time.Sleep(10 * time.Millisecond)
	rl.GetLimiter("tenant2")
	time.Sleep(10 * time.Millisecond)

	rl.mu.RLock()
	assert.Len(t, rl.visitors, 2)
	rl.mu.RUnlock()

	// This should evict tenant1 (oldest access)
	rl.GetLimiter("tenant3")

	rl.mu.RLock()
	_, exists1 := rl.visitors["tenant1"]
	_, exists2 := rl.visitors["tenant2"]
	_, exists3 := rl.visitors["tenant3"]
	rl.mu.RUnlock()

	assert.False(t, exists1, "tenant1 should be evicted")
	assert.True(t, exists2)
	assert.True(t, exists3)
}

func TestTenantRateLimiter_Cleanup(t *testing.T) {
	// TTL 50ms, cleanup interval 10ms
	rl := NewTenantRateLimiter(rate.Limit(10), 20, 100, 50*time.Millisecond, 10*time.Millisecond)

	rl.GetLimiter("tenant1")
	// The cleanup goroutine ticks every 10ms — guard the read.
	rl.mu.RLock()
	assert.Len(t, rl.visitors, 1)
	rl.mu.RUnlock()

	// Wait for cleanup
	time.Sleep(150 * time.Millisecond)

	rl.mu.RLock()
	assert.Empty(t, rl.visitors, "limiter should be cleaned up after TTL")
	rl.mu.RUnlock()
}

// TestTenantRateLimiter_ConcurrentAccess exercises the hot read path
// against concurrent eviction-by-insert. Run with -race: the old
// RUnlock→Lock upgrade pattern made lastAccess updates land on entries
// already evicted from the map, skewing LRU order. With atomic
// timestamps the only shared mutable state outside the lock is the
// atomic itself.
func TestTenantRateLimiter_ConcurrentAccess(t *testing.T) {
	rl := NewTenantRateLimiter(rate.Limit(1000), 1000, 4, time.Minute, time.Minute)

	done := make(chan struct{})
	for g := 0; g < 8; g++ {
		go func(g int) {
			defer func() { done <- struct{}{} }()
			tenants := []string{"a", "b", "c", "d", "e", "f"}
			for i := 0; i < 500; i++ {
				l := rl.GetLimiter(tenants[(g+i)%len(tenants)])
				if l == nil {
					t.Error("GetLimiter returned nil")
					return
				}
			}
		}(g)
	}
	for g := 0; g < 8; g++ {
		<-done
	}

	rl.mu.RLock()
	n := len(rl.visitors)
	rl.mu.RUnlock()
	if n > 4 {
		t.Errorf("visitors above maxEntries: %d > 4", n)
	}
}

func TestTenantRateLimiter_Stats(t *testing.T) {
	rl := NewTenantRateLimiter(rate.Limit(10), 20, 100, time.Minute, time.Minute)
	rl.GetLimiter("tenant1")
	rl.GetLimiter("tenant2")

	stats := rl.Stats()
	assert.Equal(t, 2, stats["active_limiters"])
	assert.Equal(t, 100, stats["max_entries"])
}
