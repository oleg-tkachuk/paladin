package capability

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/google/uuid"
)

// Revoking one copy of a Biscuit.
//
// Every block of a Biscuit carries a revocation id — its signature, which
// biscuit-go reports authority first. A token stops verifying when any of
// its ids is revoked, so revoking a block's id revokes the copy that ends
// with it and every copy attenuated from that one, and nothing above it:
// the copy it was attenuated from, its siblings, and the capability's JWT
// keep working. Revoking the authority block's id — the last id of an
// unattenuated Biscuit — revokes that Biscuit and every copy attenuated from
// it, but not the capability's JWT, nor another Biscuit minted for the same
// capability (each has its own root). Revoking the capability itself
// (Store.Revoke) revokes them all.

// BiscuitRevocationLookup answers whether any of a Biscuit's revocation ids
// is revoked. The ids it is asked about come from a Biscuit whose signature
// chain has already verified.
type BiscuitRevocationLookup interface {
	IsBiscuitRevoked(ctx context.Context, revocationIDs [][]byte) (bool, error)
}

// BiscuitRevocationStore persists revoked Biscuit copies. It is separate from
// Store so that a consumer without Biscuits implements nothing more.
type BiscuitRevocationStore interface {
	BiscuitRevocationLookup

	// RevokeBiscuit lists one revocation id of a capability's Biscuit.
	// Idempotent. Returns ErrNotFound when the capability is not on record
	// — or not visible to the caller — so a caller cannot revoke a copy of a
	// capability it cannot see, and an ErrInvalidRequest for an empty
	// revocation id.
	RevokeBiscuit(ctx context.Context, req RevokeBiscuitRequest) error
}

// RevokeBiscuitRequest is the input shape for BiscuitRevocationStore.RevokeBiscuit.
type RevokeBiscuitRequest struct {
	// CapabilityID is the capability the Biscuit seals.
	CapabilityID uuid.UUID
	// RevocationID is the id to list: BiscuitCopy.RevocationID.
	RevocationID []byte
	// Reason and Actor are stored for audit, as on RevokeRequest.
	Reason string
	Actor  string
}

// BiscuitCopy identifies one copy of a capability's Biscuit.
type BiscuitCopy struct {
	// CapabilityID is the capability the Biscuit seals.
	CapabilityID uuid.UUID
	// RevocationID is the id of the copy's last block: listing it revokes
	// this copy and every copy attenuated from it.
	RevocationID []byte
	// Limits are the request and budget limits in force on the copy — its
	// own and those of the copies it was attenuated from — innermost first;
	// CopyUsageReader reads their counters.
	Limits []CopyCeiling
}

// BiscuitCopy reads which copy token is, after checking it as Verify does —
// the sealed token's signature, issuer and tenant, then the Biscuit's
// signature chain — so that a revocation is only ever recorded against a
// capability the token genuinely carries. Expiry, audience and revocation are
// not checked: a copy can be revoked whatever its state. Nor is MeterCopies:
// a copy carrying limits is named, and can be revoked, on any verifier.
func (v *StandardVerifier) BiscuitCopy(ctx context.Context, token string) (BiscuitCopy, error) {
	if !IsBiscuit(token) {
		return BiscuitCopy{}, fmt.Errorf("%w: not a Biscuit token", ErrInvalidSignature)
	}
	if len(token) > v.cfg.MaxTokenBytes {
		return BiscuitCopy{}, fmt.Errorf("%w: token is %d bytes (limit %d)",
			ErrInvalidSignature, len(token), v.cfg.MaxTokenBytes)
	}
	c, ids, err := v.verifyBiscuit(ctx, token, true)
	if err != nil {
		return BiscuitCopy{}, err
	}
	return BiscuitCopy{CapabilityID: c.ID, RevocationID: ids[len(ids)-1], Limits: c.Copies}, nil
}

// CachedBiscuitRevocationChecker caches IsBiscuitRevoked answers per token —
// keyed by its whole list of revocation ids — under the same TTL and bound as
// CachedRevocationChecker, and is cleared the same way.
type CachedBiscuitRevocationChecker struct {
	upstream   BiscuitRevocationLookup
	ttl        time.Duration
	maxEntries int
	now        func() time.Time

	mu      sync.Mutex
	entries map[[sha256.Size]byte]cacheEntry
}

// NewCachedBiscuitRevocationChecker wires a cache over an upstream lookup.
// ttl follows NewCachedRevocationChecker: 0 is the default, < 0 disables
// caching. WithCacheClock and WithMaxEntries apply as they do there.
func NewCachedBiscuitRevocationChecker(upstream BiscuitRevocationLookup, ttl time.Duration, opts ...CacheOption) *CachedBiscuitRevocationChecker {
	// The options are written against CachedRevocationChecker; read them
	// off one rather than keeping a second option type.
	base := NewCachedRevocationChecker(nil, ttl, opts...)
	return &CachedBiscuitRevocationChecker{
		upstream:   upstream,
		ttl:        base.ttl,
		maxEntries: base.maxEntries,
		now:        base.now,
		entries:    make(map[[sha256.Size]byte]cacheEntry),
	}
}

// IsBiscuitRevoked serves a fresh cached answer or asks upstream. An upstream
// error is returned and never cached.
func (c *CachedBiscuitRevocationChecker) IsBiscuitRevoked(ctx context.Context, revocationIDs [][]byte) (bool, error) {
	if c.ttl < 0 {
		return c.upstream.IsBiscuitRevoked(ctx, revocationIDs)
	}
	key := biscuitCacheKey(revocationIDs)
	c.mu.Lock()
	if e, ok := c.entries[key]; ok && e.expires.After(c.now()) {
		c.mu.Unlock()
		return e.revoked, nil
	}
	c.mu.Unlock()

	revoked, err := c.upstream.IsBiscuitRevoked(ctx, revocationIDs)
	if err != nil {
		return false, err
	}
	c.mu.Lock()
	if len(c.entries) >= c.maxEntries {
		now := c.now()
		for k, e := range c.entries {
			if !e.expires.After(now) {
				delete(c.entries, k)
			}
		}
		if len(c.entries) >= c.maxEntries {
			clear(c.entries)
		}
	}
	c.entries[key] = cacheEntry{revoked: revoked, expires: c.now().Add(c.ttl)}
	c.mu.Unlock()
	return revoked, nil
}

// Clear drops every entry; wire it to the same revocation notification as
// CachedRevocationChecker.Clear.
func (c *CachedBiscuitRevocationChecker) Clear() {
	c.mu.Lock()
	clear(c.entries)
	c.mu.Unlock()
}

// biscuitCacheKey hashes the ids length-prefixed, so no two lists share a key.
func biscuitCacheKey(ids [][]byte) [sha256.Size]byte {
	h := sha256.New()
	for _, id := range ids {
		h.Write(binary.BigEndian.AppendUint64(nil, uint64(len(id))))
		h.Write(id)
	}
	var out [sha256.Size]byte
	copy(out[:], h.Sum(nil))
	return out
}

// errNoBiscuitRevocations is returned by NewStandardVerifier when Biscuits
// are accepted with nothing to check their copies against.
var errNoBiscuitRevocations = errors.New("capability: VerifierConfig.BiscuitRevocations required when AcceptBiscuit is set")
