package auth

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/capability"
)

const taintedURI = "object://t/c/flagged.txt"

func taintCtx(cap *capability.Capability, lookup TaintLookup, calls *int) context.Context {
	counting := func(ctx context.Context, uri string) (bool, error) {
		*calls++
		return lookup(ctx, uri)
	}
	return withTaintLookup(WithCapability(context.Background(), cap), counting)
}

func flaggedOnly(_ context.Context, uri string) (bool, error) { return uri == taintedURI, nil }

func TestAssertCapabilityOpRefusesTaintedReads(t *testing.T) {
	cap := &capability.Capability{ID: uuid.New(), Caveats: capability.Caveats{
		Ops: []capability.Op{capability.OpGet, capability.OpPut},
	}}
	calls := 0
	ctx := taintCtx(cap, flaggedOnly, &calls)

	err := AssertCapabilityOp(ctx, capability.OpGet, taintedURI)
	if connect.CodeOf(err) != connect.CodePermissionDenied || !errors.Is(err, capability.ErrTaintedReadNotAllowed) {
		t.Fatalf("tainted read: err = %v, want PermissionDenied wrapping ErrTaintedReadNotAllowed", err)
	}
	if err := AssertCapabilityOp(ctx, capability.OpGet, "object://t/c/clean.txt"); err != nil {
		t.Fatalf("clean read: %v", err)
	}
	// A write replaces the content; the taint caveat is about reading it.
	before := calls
	if err := AssertCapabilityOp(ctx, capability.OpPut, taintedURI); err != nil {
		t.Fatalf("write to a tainted object: %v", err)
	}
	if calls != before {
		t.Error("a mutating op looked the taint up; it cannot be refused by it")
	}
}

func TestAssertCapabilityOpAllowTaintedReadSkipsTheLookup(t *testing.T) {
	cap := &capability.Capability{ID: uuid.New(), Caveats: capability.Caveats{
		Ops: []capability.Op{capability.OpGet}, AllowTaintedRead: true,
	}}
	calls := 0
	if err := AssertCapabilityOp(taintCtx(cap, flaggedOnly, &calls), capability.OpGet, taintedURI); err != nil {
		t.Fatalf("tainted read with AllowTaintedRead: %v", err)
	}
	if calls != 0 {
		t.Errorf("lookup ran %d times for a capability that may read tainted content", calls)
	}
}

// "Could not tell" is not "clean".
func TestAssertCapabilityOpFailsClosedWhenTheLookupFails(t *testing.T) {
	cap := &capability.Capability{ID: uuid.New(), Caveats: capability.Caveats{Ops: []capability.Op{capability.OpGet}}}
	broken := func(context.Context, string) (bool, error) { return false, errors.New("db down") }
	calls := 0
	err := AssertCapabilityOp(taintCtx(cap, broken, &calls), capability.OpGet, taintedURI)
	if connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("lookup failure: err = %v, want Unavailable", err)
	}
}

// The lookup costs a query, so a request the other caveats already refuse
// never reaches it.
func TestAssertCapabilityOpChecksCheapCaveatsFirst(t *testing.T) {
	cap := &capability.Capability{ID: uuid.New(), Caveats: capability.Caveats{Ops: []capability.Op{capability.OpList}}}
	calls := 0
	if err := AssertCapabilityOp(taintCtx(cap, flaggedOnly, &calls), capability.OpGet, taintedURI); !errors.Is(err, capability.ErrOpNotAllowed) {
		t.Fatalf("err = %v, want ErrOpNotAllowed", err)
	}
	if calls != 0 {
		t.Errorf("lookup ran for a request the op caveat refused")
	}
}
