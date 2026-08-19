package s3adapter

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/oleg-tkachuk/paladin/internal/config"
)

// testRegistry returns a registry over two backends whose build is a
// counting fake, so tests exercise caching / routing without AWS.
func testRegistry() (*BackendRegistry, *int32) {
	cfg := config.Storage{
		Backends: map[string]config.StorageBackend{
			"primary":   {},
			"secondary": {},
		},
	}
	reg := NewBackendRegistry(cfg)
	var builds int32
	reg.build = func(context.Context, config.StorageBackend) (*Client, error) {
		atomic.AddInt32(&builds, 1)
		return &Client{}, nil // fresh instance per build → caching is observable
	}
	return reg, &builds
}

func TestBackendRegistry_ForBuildsOncePerID(t *testing.T) {
	reg, builds := testRegistry()
	ctx := context.Background()

	a1, err := reg.For(ctx, "primary")
	if err != nil {
		t.Fatalf("For(primary): %v", err)
	}
	a2, err := reg.For(ctx, "primary")
	if err != nil {
		t.Fatalf("For(primary) #2: %v", err)
	}
	if a1 != a2 {
		t.Fatal("For(primary) returned a different client on the second call — not cached")
	}
	b, err := reg.For(ctx, "secondary")
	if err != nil {
		t.Fatalf("For(secondary): %v", err)
	}
	if b == a1 {
		t.Fatal("distinct backends returned the same client")
	}
	if got := atomic.LoadInt32(builds); got != 2 {
		t.Fatalf("build called %d times, want 2 (one per distinct id)", got)
	}
}

func TestBackendRegistry_UnknownIDErrors(t *testing.T) {
	reg, builds := testRegistry()
	if _, err := reg.For(context.Background(), "nope"); err == nil {
		t.Fatal("For(unknown) returned nil error — must not silently default")
	}
	if got := atomic.LoadInt32(builds); got != 0 {
		t.Fatalf("build called %d times for an unknown id, want 0", got)
	}
}

func TestBackendRegistry_EmptyIDErrors(t *testing.T) {
	reg, builds := testRegistry()
	if _, err := reg.For(context.Background(), ""); err == nil {
		t.Fatal("For(\"\") returned nil error — there is no default backend; an empty id must be rejected")
	}
	if got := atomic.LoadInt32(builds); got != 0 {
		t.Fatalf("build called %d times for an empty id, want 0", got)
	}
}

func TestBackendRegistry_ConcurrentForBuildsOnce(t *testing.T) {
	reg, builds := testRegistry()
	var wg sync.WaitGroup
	got := make([]*Client, 32)
	for i := range got {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			c, err := reg.For(context.Background(), "primary")
			if err != nil {
				t.Errorf("For: %v", err)
				return
			}
			got[i] = c
		}(i)
	}
	wg.Wait()

	if n := atomic.LoadInt32(builds); n != 1 {
		t.Fatalf("build called %d times under concurrency, want 1", n)
	}
	for i, c := range got {
		if c != got[0] {
			t.Fatalf("goroutine %d got a different client instance", i)
		}
	}
}

func TestBackendRegistry_Warmup(t *testing.T) {
	reg, builds := testRegistry()
	if err := reg.Warmup(context.Background()); err != nil {
		t.Fatalf("Warmup: %v", err)
	}
	if got := atomic.LoadInt32(builds); got != 2 {
		t.Fatalf("Warmup built %d backends, want 2 (all configured)", got)
	}
}

func TestBackendRegistry_WarmupNoBackends(t *testing.T) {
	reg := NewBackendRegistry(config.Storage{Backends: map[string]config.StorageBackend{}})
	if err := reg.Warmup(context.Background()); err == nil {
		t.Fatal("Warmup with zero configured backends returned nil error")
	}
}

func TestBackendRegistry_Invalidate(t *testing.T) {
	reg, builds := testRegistry()
	ctx := context.Background()

	first, err := reg.For(ctx, "primary")
	if err != nil {
		t.Fatalf("For: %v", err)
	}
	reg.Invalidate("primary")
	second, err := reg.For(ctx, "primary")
	if err != nil {
		t.Fatalf("For after Invalidate: %v", err)
	}
	if first == second {
		t.Fatal("Invalidate did not force a rebuild — same client returned")
	}
	if got := atomic.LoadInt32(builds); got != 2 {
		t.Fatalf("build called %d times, want 2 (rebuild after Invalidate)", got)
	}
}
