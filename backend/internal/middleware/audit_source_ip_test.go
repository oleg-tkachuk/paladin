package middleware

import (
	"context"
	"net/netip"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/clientip"
)

// The audit row's source is the address the listener resolved from its
// trusted proxies. It used to be the raw X-Forwarded-For value, whose leftmost
// entry the caller writes — so the trail recorded whatever source the caller
// claimed.
func TestSourceIPIsTheResolvedAddress(t *testing.T) {
	t.Parallel()
	ctx := clientip.WithAddr(context.Background(), netip.MustParseAddr("198.51.100.7"))
	if got := sourceIP(ctx); got != "198.51.100.7" {
		t.Errorf("sourceIP = %q, want 198.51.100.7", got)
	}
	if got := sourceIP(context.Background()); got != "" {
		t.Errorf("sourceIP with no resolved address = %q, want empty", got)
	}
}
