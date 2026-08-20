package eventingest

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// fakePrefixBackend is a controllable prefixBackend that counts list calls so
// tests can assert cache hits vs backend round-trips.
type fakePrefixBackend struct {
	keys      []string
	listCalls int
	listErr   error

	lookupCalls int
	getCalls    int
}

func (f *fakePrefixBackend) ListCollectionNamesForTenant(_ context.Context, _ pgtype.UUID) ([]string, error) {
	f.listCalls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	// Return a copy so a caller can't mutate the backing set.
	out := make([]string, len(f.keys))
	copy(out, f.keys)
	return out, nil
}

func (f *fakePrefixBackend) LookupObjectByKey(_ context.Context, _ pgtype.UUID, _, _ string) (sqlc.LookupObjectByKeyRow, error) {
	f.lookupCalls++
	return sqlc.LookupObjectByKeyRow{}, nil
}

func (f *fakePrefixBackend) GetCollection(_ context.Context, _ pgtype.UUID, _ string) (sqlc.GetCollectionRow, error) {
	f.getCalls++
	return sqlc.GetCollectionRow{}, nil
}

func testTenant() pgtype.UUID {
	return pgtype.UUID{Bytes: uuid.New(), Valid: true}
}

// TestCachingLookup_EvictsPastCap: the per-tenant map is size-bounded — loading
// more distinct tenants than maxPrefixCacheTenants must not grow it without
// limit (memory backstop, mirrors the CEL cache).
func TestCachingLookup_EvictsPastCap(t *testing.T) {
	be := &fakePrefixBackend{keys: []string{"invoices"}}
	c := NewCachingLookup(be, time.Minute, nil)

	for i := 0; i < maxPrefixCacheTenants+100; i++ {
		if _, _, err := c.keysFor(context.Background(), testTenant(), false); err != nil {
			t.Fatalf("keysFor #%d: %v", i, err)
		}
	}

	c.mu.Lock()
	n := len(c.cache)
	c.mu.Unlock()
	if n > maxPrefixCacheTenants {
		t.Errorf("cache holds %d entries, want <= %d (eviction didn't fire)", n, maxPrefixCacheTenants)
	}
}

func TestCachingLookup_LongestPrefixWins(t *testing.T) {
	be := &fakePrefixBackend{keys: []string{"invoices", "invoices/2026/q1", "photos"}}
	c := NewCachingLookup(be, time.Minute, nil)
	tid := testTenant()

	got, err := c.ResolveCollectionPrefix(context.Background(), tid, "invoices/2026/q1/report.pdf")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "invoices/2026/q1" {
		t.Errorf("longest-prefix = %q, want invoices/2026/q1", got)
	}

	// A tail that only the shorter OK prefixes resolves to it.
	got, err = c.ResolveCollectionPrefix(context.Background(), tid, "invoices/legacy.pdf")
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if got != "invoices" {
		t.Errorf("prefix = %q, want invoices", got)
	}
}

func TestCachingLookup_CacheHitSkipsBackend(t *testing.T) {
	be := &fakePrefixBackend{keys: []string{"photos"}}
	c := NewCachingLookup(be, time.Minute, nil)
	tid := testTenant()

	for i := 0; i < 5; i++ {
		if _, err := c.ResolveCollectionPrefix(context.Background(), tid, "photos/a.jpg"); err != nil {
			t.Fatalf("resolve #%d: %v", i, err)
		}
	}
	if be.listCalls != 1 {
		t.Errorf("backend list calls = %d, want 1 (4 subsequent resolves served from cache)", be.listCalls)
	}
}

func TestCachingLookup_TTLExpiryReloads(t *testing.T) {
	be := &fakePrefixBackend{keys: []string{"photos"}}
	c := NewCachingLookup(be, 30*time.Second, nil)
	now := time.Unix(1000, 0)
	c.now = func() time.Time { return now }
	tid := testTenant()

	if _, err := c.ResolveCollectionPrefix(context.Background(), tid, "photos/a.jpg"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	// Within TTL → cache hit.
	now = now.Add(29 * time.Second)
	if _, err := c.ResolveCollectionPrefix(context.Background(), tid, "photos/a.jpg"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if be.listCalls != 1 {
		t.Fatalf("within-TTL list calls = %d, want 1", be.listCalls)
	}
	// Past TTL → reload.
	now = now.Add(2 * time.Second)
	if _, err := c.ResolveCollectionPrefix(context.Background(), tid, "photos/a.jpg"); err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if be.listCalls != 2 {
		t.Errorf("post-TTL list calls = %d, want 2 (reload)", be.listCalls)
	}
}

func TestCachingLookup_RefreshOnMissPicksUpNewKey(t *testing.T) {
	be := &fakePrefixBackend{keys: []string{"photos"}}
	c := NewCachingLookup(be, time.Minute, nil)
	tid := testTenant()

	// Warm the cache with a matching resolve (photos), so the next lookup for a
	// brand-new top-level key starts from a populated-but-stale cache.
	if _, err := c.ResolveCollectionPrefix(context.Background(), tid, "photos/a.jpg"); err != nil {
		t.Fatalf("warm: %v", err)
	}
	if be.listCalls != 1 {
		t.Fatalf("warm list calls = %d, want 1", be.listCalls)
	}

	// A new OK is registered (in another pod) well inside the TTL. The cache is
	// stale, so the first resolve misses — the refresh-on-miss must reload and
	// resolve it rather than wait out the TTL.
	be.keys = []string{"photos", "invoices"}
	got, err := c.ResolveCollectionPrefix(context.Background(), tid, "invoices/2026.pdf")
	if err != nil {
		t.Fatalf("resolve new key: %v", err)
	}
	if got != "invoices" {
		t.Errorf("new-key resolve = %q, want invoices", got)
	}
	if be.listCalls != 2 {
		t.Errorf("list calls = %d, want 2 (initial + one forced refresh on miss)", be.listCalls)
	}
}

func TestCachingLookup_NoMatchIsErrNoRows(t *testing.T) {
	be := &fakePrefixBackend{keys: []string{"photos"}}
	c := NewCachingLookup(be, time.Minute, nil)
	tid := testTenant()

	_, err := c.ResolveCollectionPrefix(context.Background(), tid, "unregistered/x.txt")
	if !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("no-match err = %v, want pgx.ErrNoRows", err)
	}
	// A cold cache loads once; the miss then forces one refresh (fresh=false on
	// the first load path is only set after load, so cold-load counts as fresh
	// and skips the redundant refresh).
	if be.listCalls != 1 {
		t.Errorf("cold-miss list calls = %d, want 1 (no redundant refresh after a fresh load)", be.listCalls)
	}
}

func TestCachingLookup_BackendErrorPropagates(t *testing.T) {
	be := &fakePrefixBackend{listErr: errors.New("db down")}
	c := NewCachingLookup(be, time.Minute, nil)

	_, err := c.ResolveCollectionPrefix(context.Background(), testTenant(), "photos/a.jpg")
	if err == nil || errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("err = %v, want the backend error propagated", err)
	}
}

func TestCachingLookup_PassthroughDelegates(t *testing.T) {
	be := &fakePrefixBackend{}
	c := NewCachingLookup(be, time.Minute, nil)
	tid := testTenant()

	if _, err := c.LookupObjectByKey(context.Background(), tid, "ok", "k"); err != nil {
		t.Fatalf("lookup: %v", err)
	}
	if _, err := c.GetCollection(context.Background(), tid, "ok"); err != nil {
		t.Fatalf("get: %v", err)
	}
	if be.lookupCalls != 1 || be.getCalls != 1 {
		t.Errorf("passthrough calls = (lookup %d, get %d), want (1, 1)", be.lookupCalls, be.getCalls)
	}
	if be.listCalls != 0 {
		t.Errorf("passthrough triggered %d list calls, want 0", be.listCalls)
	}
}

// The concrete *sqlc.Queries must satisfy prefixBackend so the production wiring
// in serve_ingest.go compiles.
var _ prefixBackend = (*sqlc.Queries)(nil)
