package apiutil_test

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/oleg-tkachuk/paladin/backend/internal/api/apiutil"
)

const ttlField = "ttl_seconds"

func TestTTLOf(t *testing.T) {
	for _, tc := range []struct {
		seconds int64
		want    time.Duration
		ok      bool
	}{
		{0, 0, true},
		{300, 5 * time.Minute, true},
		{apiutil.MaxTTLSeconds, time.Duration(apiutil.MaxTTLSeconds) * time.Second, true},
		{apiutil.MaxTTLSeconds + 1, 0, false},
		{1 << 62, 0, false},
	} {
		got, err := apiutil.TTLOf(ttlField, tc.seconds)
		if (err == nil) != tc.ok || got != tc.want {
			t.Errorf("TTLOf(%d) = %v, %v; want %v, ok=%v", tc.seconds, got, err, tc.want, tc.ok)
		}
		if err == nil {
			continue
		}
		if !strings.HasPrefix(err.Error(), ttlField+": ") {
			t.Errorf("TTLOf(%d) = %q, want it to name %s", tc.seconds, err, ttlField)
		}
		if !strings.Contains(err.Error(), strconv.FormatInt(apiutil.MaxTTLSeconds, 10)) {
			t.Errorf("TTLOf(%d) = %q, want it to name the limit", tc.seconds, err)
		}
	}
}
