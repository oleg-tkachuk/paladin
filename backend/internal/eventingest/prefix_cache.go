package eventingest

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// DefaultPrefixCacheTTL bounds how stale the per-tenant collection set backing
// ResolveCollectionPrefix may be. See CachingLookup for the staleness contract.
const DefaultPrefixCacheTTL = 30 * time.Second

// maxPrefixCacheTenants bounds the number of per-tenant entries the cache
// holds. Tenant cardinality on a control plane is bounded, so eviction
// effectively never fires in normal operation — this is a memory backstop
// against a runaway tenant count (or a test/synthetic burst), mirroring the CEL
// evaluator's bounded compile cache. At capacity a new tenant evicts one
// arbitrary entry (its next resolve just reloads from the backend — cheap).
const maxPrefixCacheTenants = 2048

// prefixBackend is the store surface CachingLookup needs: the two object
// lookups it passes straight through, plus the full per-tenant collection name
// list it caches for longest-prefix resolution. *sqlc.Queries satisfies it.
type prefixBackend interface {
	LookupObjectByKey(ctx context.Context, tenantID pgtype.UUID, collection, key string) (sqlc.LookupObjectByKeyRow, error)
	GetCollection(ctx context.Context, tenantID pgtype.UUID, collection string) (sqlc.GetCollectionRow, error)
	ListCollectionNamesForTenant(ctx context.Context, tenantID pgtype.UUID) ([]string, error)
}

// CachingLookup is an ObjectLookup that resolves the longest-prefix collection
// (multi-segment disambiguation, the schema baseline (001_initial_schema.sql)) from an in-process per-tenant
// cache instead of a per-event SQL query. LookupObjectByKey / GetCollection pass
// straight through — only ResolveCollectionPrefix is cached.
//
// Staleness contract: the cached collection set for a tenant is at most `ttl`
// old. collection create/delete happens in the admin pod while this cache lives
// in the ingest/dispatcher pod, so there is no cross-pod invalidation — TTL is
// the only freshness bound. Two consequences, both benign:
//
//   - A brand-new collection that is NOT nested under an already-cached key is
//     picked up immediately: a resolve miss forces one refresh before giving up
//     (see ResolveCollectionPrefix), so the first storage event to a new
//     top-level OK resolves without waiting out the TTL.
//   - A brand-new collection nested UNDER an already-cached key (e.g. adding
//     `invoices/2026/q1` while `invoices` is cached) can, within the TTL window,
//     resolve to the shorter cached parent. The subsequent LookupObjectByKey
//     then finds no row and the event is skipped — the data-plane Reconciler
//     promotes the orphaned PENDING object, so the upload is eventually
//     consistent, not lost. This is the same eventual-consistency posture the
//     handler already relies on for the presign/upload race.
//
// The cache map holds one small string slice per tenant. Entries are refreshed
// in place; the map is size-bounded at maxPrefixCacheTenants (arbitrary-eviction
// backstop, like the CEL cache) so a runaway tenant count can't grow it without
// limit. Tenant cardinality on a control plane is bounded, so eviction
// effectively never fires in normal operation.
type CachingLookup struct {
	src prefixBackend
	ttl time.Duration
	log *zap.Logger

	mu    sync.Mutex
	cache map[pgtype.UUID]cachedKeys
	// now is overridable in tests; nil → time.Now.
	now func() time.Time
}

type cachedKeys struct {
	keys []string
	at   time.Time
}

// NewCachingLookup wraps a backend (typically *sqlc.Queries) with the
// per-tenant longest-prefix cache. ttl <= 0 falls back to DefaultPrefixCacheTTL;
// a nil logger is tolerated.
func NewCachingLookup(src prefixBackend, ttl time.Duration, log *zap.Logger) *CachingLookup {
	if ttl <= 0 {
		ttl = DefaultPrefixCacheTTL
	}
	if log == nil {
		log = zap.NewNop()
	}
	return &CachingLookup{
		src:   src,
		ttl:   ttl,
		log:   log,
		cache: map[pgtype.UUID]cachedKeys{},
	}
}

// LookupObjectByKey passes through untouched — object resolution stays a direct
// query keyed by the (tenant, collection, key) unique index.
func (c *CachingLookup) LookupObjectByKey(ctx context.Context, tenantID pgtype.UUID, collection, key string) (sqlc.LookupObjectByKeyRow, error) {
	return c.src.LookupObjectByKey(ctx, tenantID, collection, key)
}

// GetCollection passes through untouched.
func (c *CachingLookup) GetCollection(ctx context.Context, tenantID pgtype.UUID, collection string) (sqlc.GetCollectionRow, error) {
	return c.src.GetCollection(ctx, tenantID, collection)
}

// ResolveCollectionPrefix returns the longest registered collection that is a
// "/"-delimited prefix of `tail`, resolved from the cached per-tenant key set.
// pgx.ErrNoRows when none match — the same contract the SQL query has, so the
// handler's switch is unchanged.
func (c *CachingLookup) ResolveCollectionPrefix(ctx context.Context, tenantID pgtype.UUID, tail string) (string, error) {
	keys, fresh, err := c.keysFor(ctx, tenantID, false)
	if err != nil {
		return "", err
	}
	if m := longestPrefixMatch(keys, tail); m != "" {
		return m, nil
	}
	// Miss on a possibly-stale cache: a newly-registered top-level collection
	// wouldn't be here yet. Unless we just loaded fresh, force one refresh and
	// retry so a new OK's first event resolves without waiting out the TTL.
	if !fresh {
		keys, _, err = c.keysFor(ctx, tenantID, true)
		if err != nil {
			return "", err
		}
		if m := longestPrefixMatch(keys, tail); m != "" {
			return m, nil
		}
	}
	return "", pgx.ErrNoRows
}

// keysFor returns the tenant's collection set, loading from the backend when the
// cache is cold, expired, or force is set. `fresh` reports whether this call hit
// the backend (vs served the cache) so the caller can avoid a redundant refresh.
func (c *CachingLookup) keysFor(ctx context.Context, tenantID pgtype.UUID, force bool) (keys []string, fresh bool, err error) {
	now := c.clock()

	c.mu.Lock()
	entry, ok := c.cache[tenantID]
	stale := !ok || now.Sub(entry.at) >= c.ttl
	if !force && !stale {
		keys = entry.keys
		c.mu.Unlock()
		return keys, false, nil
	}
	c.mu.Unlock()

	// Load outside the lock — the SQL round-trip must not block other tenants'
	// resolves. A concurrent duplicate load for the same tenant is acceptable
	// (idempotent, rare) and simpler than single-flight bookkeeping.
	loaded, err := c.src.ListCollectionNamesForTenant(ctx, tenantID)
	if err != nil {
		return nil, false, err
	}

	c.mu.Lock()
	if _, present := c.cache[tenantID]; !present && len(c.cache) >= maxPrefixCacheTenants {
		// At capacity and this is a new tenant — evict one arbitrary entry to
		// bound memory (mirror the CEL cache). Range yields a pseudo-random key;
		// delete-during-range is safe in Go. The evicted tenant just reloads on
		// its next resolve.
		for k := range c.cache {
			delete(c.cache, k)
			break
		}
	}
	c.cache[tenantID] = cachedKeys{keys: loaded, at: now}
	c.mu.Unlock()
	return loaded, true, nil
}

func (c *CachingLookup) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// longestPrefixMatch returns the longest key in `keys` that equals `tail` or is
// a "/"-delimited prefix of it — the same precedence the ResolveCollectionPrefix
// SQL gives (ORDER BY length(collection) DESC). collection is ASCII, so byte
// length equals character length. "" when none match.
func longestPrefixMatch(keys []string, tail string) string {
	best := ""
	for _, k := range keys {
		if k == tail || strings.HasPrefix(tail, k+"/") {
			if len(k) > len(best) {
				best = k
			}
		}
	}
	return best
}
