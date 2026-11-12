package session

import (
	"strconv"
	"testing"
	"time"
)

func TestAccessCacheKeysBySessionAndAudience(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	c := newAccessCache(func() time.Time { return now })
	c.put("r1", "paladin-data", "t-data", time.Hour)
	if got, ok := c.get("r1", "paladin-data"); !ok || got != "t-data" {
		t.Errorf("get = %q %v", got, ok)
	}
	for _, miss := range [][2]string{{"r1", "paladin-admin"}, {"r2", "paladin-data"}} {
		if _, ok := c.get(miss[0], miss[1]); ok {
			t.Errorf("get(%v) hit, want a miss", miss)
		}
	}
	c.forget("r1")
	if _, ok := c.get("r1", "paladin-data"); ok {
		t.Error("a forgotten session's token is still served")
	}
}

func TestAccessCacheNeverServesATokenAboutToExpire(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	c := newAccessCache(func() time.Time { return now })
	c.put("r", "a", "short", accessEarlyExpiry)
	if _, ok := c.get("r", "a"); ok {
		t.Error("a token with no time left past the early expiry was cached")
	}
	c.put("r", "a", "long", time.Hour)
	now = now.Add(time.Hour - accessEarlyExpiry)
	if _, ok := c.get("r", "a"); ok {
		t.Error("a token was served inside its early-expiry window")
	}
}

func TestAccessCacheStaysBounded(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	c := newAccessCache(func() time.Time { return now })
	for i := range maxCachedAccess + 1 {
		c.put(strconv.Itoa(i), "a", "t", time.Hour)
	}
	if len(c.entries) > maxCachedAccess {
		t.Errorf("%d entries, want at most %d", len(c.entries), maxCachedAccess)
	}
}
