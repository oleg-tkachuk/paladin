package config

import (
	"fmt"
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/uploadpolicy"
)

// SigV4MaxPresignExpiry is the longest TTL max_ttl may name; enforced at load
// because a longer URL signs fine and fails only when somebody uses it.
const SigV4MaxPresignExpiry = uploadpolicy.SigV4MaxPresignExpiry

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
	return ValidatePresignTTLs(p.GetTTL, p.PutTTL, p.PartTTL, p.MaxTTL)
}

// UploadLimits is limits.* as the upload policy reads it.
func (l Limits) UploadLimits() uploadpolicy.Limits {
	return uploadpolicy.Limits{
		MaxObjectSize:       l.MaxObjectSizeBytes,
		MaxMultipartSize:    l.MaxMultipartSizeBytes,
		MinPartSize:         l.MinPartSizeBytes,
		MaxPartSize:         l.MaxPartSizeBytes,
		MaxParts:            int64(l.MaxParts),
		AllowedContentTypes: l.AllowedContentTypes,
	}
}
