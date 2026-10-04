package rpcmeta

import (
	"strings"
	"testing"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

// Every paladin RPC must resolve. This is the guard on the blank imports above.
//
// Without them the registry holds nothing, Level answers "not found" for
// everything, and its callers quietly stop stamping keys — no error, no
// failure, just a fleet of clients that no longer send a header the server
// requires. A silent registry is a silent guard, so it fails here instead.
func TestEveryRPCResolves(t *testing.T) {
	var checked int
	var missing []string
	protoregistry.GlobalFiles.RangeFiles(func(fd protoreflect.FileDescriptor) bool {
		if !strings.HasPrefix(string(fd.Package()), "paladin.") {
			return true
		}
		for i := 0; i < fd.Services().Len(); i++ {
			svc := fd.Services().Get(i)
			for j := 0; j < svc.Methods().Len(); j++ {
				proc := "/" + string(svc.FullName()) + "/" + string(svc.Methods().Get(j).Name())
				if _, found := Level(proc); !found {
					missing = append(missing, proc)
				}
				checked++
			}
		}
		return true
	})
	if checked < 100 {
		t.Fatalf("only %d RPCs in the registry — the blank imports are not linked, "+
			"and every question this package answers would be a shrug", checked)
	}
	if len(missing) > 0 {
		t.Errorf("procedures the registry cannot resolve: %v", missing)
	}
	t.Logf("resolved %d RPCs", checked)
}

// The three levels, at the two decisions. Written as a table over the LEVEL
// rather than over procedures, because only two of the three are declared in
// this tree — a test that went through real procedures could not tell
// ForbidsMemoize apart from one that also treated IDEMPOTENT as a read.
func TestLevelDecisions(t *testing.T) {
	for _, c := range []struct {
		level    descriptorpb.MethodOptions_IdempotencyLevel
		memoBan  bool
		wantsKey bool
		why      string
	}{
		{descriptorpb.MethodOptions_NO_SIDE_EFFECTS, true, false,
			"a read: replaying it serves a stale snapshot, and a key buys a row for nothing"},
		{descriptorpb.MethodOptions_IDEMPOTENT, false, false,
			"safe to repeat already; replaying its response is legitimate, and a key adds no safety"},
		{descriptorpb.MethodOptions_IDEMPOTENCY_UNKNOWN, false, true,
			"nobody has promised anything about a repeat — exactly where the key works"},
	} {
		if got := ForbidsMemoize(c.level); got != c.memoBan {
			t.Errorf("ForbidsMemoize(%v) = %v, want %v (%s)", c.level, got, c.memoBan, c.why)
		}
		// wantsKey is asserted through the exported predicate below, on real
		// procedures, since it also depends on `found`.
		_ = c.wantsKey
	}
}

// The two exported predicates, on procedures that will keep their level.
//
// GetBucket is a read and cannot become anything else. UploadObject creates an
// object and charges quota. DeleteBucket is a removal. An unknown procedure is
// the fourth case and the one with a non-obvious answer: no key, because
// guessing "probably a mutation" from a name is what the three prefix lists
// did, and what UploadObject fell out of.
func TestPredicatesOnRealProcedures(t *testing.T) {
	cases := []struct {
		proc          string
		read, wantKey bool
	}{
		{"/paladin.admin.v1.BucketService/GetBucket", true, false},
		{"/paladin.data.v1.ObjectService/UploadObject", false, true},
		{"/paladin.admin.v1.BucketService/DeleteBucket", false, false},
		{"/paladin.admin.v1.NoSuchService/NoSuchMethod", false, false},
		{"not-a-procedure", false, false},
	}
	for _, c := range cases {
		if got := IsDeclaredRead(c.proc); got != c.read {
			t.Errorf("IsDeclaredRead(%s) = %v, want %v", c.proc, got, c.read)
		}
		if got := NeedsIdempotencyKey(c.proc); got != c.wantKey {
			t.Errorf("NeedsIdempotencyKey(%s) = %v, want %v", c.proc, got, c.wantKey)
		}
	}
}

// The cache must key on the procedure, not collapse to one answer.
func TestCacheIsPerProcedure(t *testing.T) {
	a, _ := Level("/paladin.admin.v1.BucketService/GetBucket")
	b, _ := Level("/paladin.data.v1.ObjectService/UploadObject")
	if a == b {
		t.Fatalf("both resolved to %v — the cache is keyed wrongly", a)
	}
}
