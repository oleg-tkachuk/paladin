package auth

import (
	"context"
	"testing"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/captest"
	"github.com/oleg-tkachuk/paladin/backend/internal/capop"
)

const admittedURI = "object://t/c/a.txt"

// admittedCtx is a request presenting cap, with the per-request holders the
// interceptor installs.
func admittedCtx(t *testing.T, cap limes.Capability) context.Context {
	t.Helper()
	return withLastOpHolder(WithCapability(context.Background(), captest.Verified(t, cap)))
}

// The policy engine grants a capability only the actions of the operation
// its caveats admitted (context.capability_op), so the operation must reach
// capop exactly when AssertCapabilityOp passes, and never when it refuses.
func TestAssertCapabilityOp_StampsOnlyWhatItAdmits(t *testing.T) {
	cap := limes.Capability{Caveats: limes.Caveats{Ops: []limes.Op{limes.OpGet}}}

	t.Run("admitted", func(t *testing.T) {
		ctx := admittedCtx(t, cap)
		if err := AssertCapabilityOp(ctx, limes.OpGet, admittedURI); err != nil {
			t.Fatalf("AssertCapabilityOp(get): %v", err)
		}
		if got := capop.Admitted(ctx); got != string(limes.OpGet) {
			t.Fatalf("admitted = %q, want %q", got, limes.OpGet)
		}
	})

	t.Run("refused", func(t *testing.T) {
		ctx := admittedCtx(t, cap)
		if err := AssertCapabilityOp(ctx, limes.OpPut, admittedURI); err == nil {
			t.Fatal("AssertCapabilityOp(put) passed a capability that allows only get")
		}
		if got := capop.Admitted(ctx); got != "" {
			t.Fatalf("admitted after a refusal = %q, want empty", got)
		}
	})

	t.Run("a refusal leaves the earlier admission", func(t *testing.T) {
		ctx := admittedCtx(t, cap)
		if err := AssertCapabilityOp(ctx, limes.OpGet, admittedURI); err != nil {
			t.Fatalf("AssertCapabilityOp(get): %v", err)
		}
		if err := AssertCapabilityOp(ctx, limes.OpPut, admittedURI); err == nil {
			t.Fatal("AssertCapabilityOp(put) passed a capability that allows only get")
		}
		if got := capop.Admitted(ctx); got != string(limes.OpGet) {
			t.Fatalf("admitted = %q, want %q", got, limes.OpGet)
		}
	})
}
