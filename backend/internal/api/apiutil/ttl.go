package apiutil

import (
	"fmt"
	"math"
	"time"
)

// MaxTTLSeconds is the longest ttl_seconds a time.Duration holds. Converted
// past it, the lifetime wraps: a request for longer is refused as a negative
// TTL, or takes the default lifetime when it wraps to zero.
const MaxTTLSeconds = int64(math.MaxInt64 / time.Second)

// TTLOf is the lifetime a count of seconds asks for, refused when a duration
// cannot hold it. field names the argument in the refusal.
func TTLOf(field string, seconds int64) (time.Duration, error) {
	if seconds > MaxTTLSeconds {
		return 0, fmt.Errorf("%s: %d is over the limit of %d", field, seconds, MaxTTLSeconds)
	}
	return time.Duration(seconds) * time.Second, nil
}
