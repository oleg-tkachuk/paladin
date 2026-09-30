package reqctx_test

import (
	"context"
	"testing"

	"github.com/oleg-tkachuk/paladin/backend/internal/reqctx"
)

func TestIDs(t *testing.T) {
	const fallback = "fallback"
	cases := []struct {
		name       string
		ctx        context.Context
		wantReq    string
		wantTenant string
	}{
		{"bare context", context.Background(), fallback, fallback},
		{"both set", reqctx.WithTenantID(reqctx.WithRequestID(context.Background(), "req-1"), "tenant-1"), "req-1", "tenant-1"},
		{"empty values fall back", reqctx.WithTenantID(reqctx.WithRequestID(context.Background(), ""), ""), fallback, fallback},
		{"only the request ID", reqctx.WithRequestID(context.Background(), "req-2"), "req-2", fallback},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := reqctx.RequestID(tc.ctx, fallback); got != tc.wantReq {
				t.Errorf("RequestID = %q, want %q", got, tc.wantReq)
			}
			if got := reqctx.TenantID(tc.ctx, fallback); got != tc.wantTenant {
				t.Errorf("TenantID = %q, want %q", got, tc.wantTenant)
			}
		})
	}
}

// A string under the same text as the key, set by another package, must not
// be read as ours: the keys are a private type.
func TestForeignKeyIsIgnored(t *testing.T) {
	type otherKey string
	ctx := context.WithValue(context.Background(), otherKey("requestID"), "spoofed")
	if got := reqctx.RequestID(ctx, ""); got != "" {
		t.Fatalf("RequestID = %q from a foreign key", got)
	}
}
