package config

import (
	"fmt"
	"time"
)

// SigV4MaxPresignExpiry is the longest X-Amz-Expires SigV4 accepts (604800
// seconds). A longer URL signs without complaint and is refused by the object
// store only when somebody uses it, so the ceiling is enforced at load.
const SigV4MaxPresignExpiry = 7 * 24 * time.Hour

// ValidatePresignTTLs checks the invariants every presign lifetime obeys:
// positive, defaults within max, max within what SigV4 can express.
func ValidatePresignTTLs(get, put, part, maxTTL time.Duration) error {
	if maxTTL <= 0 || maxTTL > SigV4MaxPresignExpiry {
		return fmt.Errorf("limits.presign.max_ttl %s must be in (0, %s]", maxTTL, SigV4MaxPresignExpiry)
	}
	for _, d := range []struct {
		key string
		v   time.Duration
	}{{"get_ttl", get}, {"put_ttl", put}, {"part_ttl", part}} {
		if d.v <= 0 || d.v > maxTTL {
			return fmt.Errorf("limits.presign.%s %s must be in (0, max_ttl %s]", d.key, d.v, maxTTL)
		}
	}
	return nil
}

// Validate checks limits.presign.
func (p Presign) Validate() error {
	if err := ValidatePresignTTLs(p.GetTTL, p.PutTTL, p.PartTTL, p.MaxTTL); err != nil {
		return err
	}
	if p.DefaultMaxSize <= 0 {
		return fmt.Errorf("limits.presign.default_max_size %d must be positive", p.DefaultMaxSize)
	}
	return nil
}
