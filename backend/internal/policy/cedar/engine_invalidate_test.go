package cedar

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// watchStore is a Store whose Watch hands back a caller-controlled channel.
type watchStore struct{ ch chan ChangeEvent }

func (w watchStore) Fetch(context.Context, uuid.UUID, string) (Layers, []byte, string, error) {
	return Layers{}, nil, "", nil
}
func (w watchStore) Watch(context.Context) (<-chan ChangeEvent, error) { return w.ch, nil }

// waitInvalidated polls until every key matching keep==false is gone.
func waitTenantEntries(t *testing.T, e *Engine, tenant uuid.UUID, want int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		n := 0
		e.compiled.Range(func(k, _ any) bool {
			if k.(cacheKey).tenant == tenant {
				n++
			}
			return true
		})
		if n == want {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("tenant %s cache entries = %d, want %d", tenant, n, want)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// A tenant-level ChangeEvent (empty Collection) must drop every cache entry
// for that tenant — the inherited policy text is concatenated into all
// collection-scoped compiles — while other tenants' entries survive. A
// scoped event drops exactly its own entry.
func TestStartInvalidation(t *testing.T) {
	events := make(chan ChangeEvent)
	e := NewEngine(watchStore{ch: events}, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := e.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	tid, other := uuid.New(), uuid.New()
	for _, k := range []cacheKey{
		{tenant: tid},
		{tenant: tid, collection: "a"},
		{tenant: tid, collection: "b"},
		{tenant: other, collection: "a"},
	} {
		e.compiled.Store(k, &compiledPolicy{})
	}

	// Scoped event → only (tid, "a") goes.
	events <- ChangeEvent{TenantID: tid, Collection: "a"}
	waitTenantEntries(t, e, tid, 2)
	if _, ok := e.compiled.Load(cacheKey{tenant: tid, collection: "b"}); !ok {
		t.Fatalf("scoped event evicted an unrelated collection entry")
	}

	// Tenant-level event → everything under tid goes, other tenant untouched.
	events <- ChangeEvent{TenantID: tid}
	waitTenantEntries(t, e, tid, 0)
	if _, ok := e.compiled.Load(cacheKey{tenant: other, collection: "a"}); !ok {
		t.Fatalf("tenant-level event evicted another tenant's entry")
	}
}

// A ResyncAll control event (emitted after the Store's LISTEN connection
// reconnects) must flush the entire compiled cache — every tenant, not just
// one — because an unknown set of invalidations was missed during the gap.
// The WatchResyncs counter increments so a flapping link is observable.
func TestStartInvalidation_ResyncAll(t *testing.T) {
	events := make(chan ChangeEvent)
	e := NewEngine(watchStore{ch: events}, time.Minute)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := e.Start(ctx); err != nil {
		t.Fatalf("start: %v", err)
	}

	a, b := uuid.New(), uuid.New()
	for _, k := range []cacheKey{
		{tenant: a},
		{tenant: a, collection: "x"},
		{tenant: b, collection: "y"},
	} {
		e.compiled.Store(k, &compiledPolicy{})
	}

	events <- ChangeEvent{ResyncAll: true}

	// Every entry, across both tenants, must be gone.
	waitTenantEntries(t, e, a, 0)
	waitTenantEntries(t, e, b, 0)

	// Counter is bumped once per resync (poll: the loop runs the flush before
	// it's observable, so give it the same slack as the cache assertions).
	deadline := time.Now().Add(5 * time.Second)
	for e.WatchResyncs() != 1 {
		if time.Now().After(deadline) {
			t.Fatalf("WatchResyncs = %d, want 1", e.WatchResyncs())
		}
		time.Sleep(5 * time.Millisecond)
	}
}
