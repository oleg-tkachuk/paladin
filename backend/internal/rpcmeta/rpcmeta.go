// Package rpcmeta answers questions about an RPC from the protobuf
// descriptor, so the answer lives in one place and comes from the contract.
//
// It replaces an identical prefix list that existed three times in two
// languages — frontend/src/lib/connect/transport.ts, cmd/seed-fixture and
// internal/mcp — each carrying a comment telling the reader to change the
// other two. That shape has cost this repository real debugging sessions
// before: the plane addresses had three spellings, the stack ports had two.
//
// The prefix lists were also WRONG in a way no amount of syncing would fix.
// They read `Create*`/`Issue*` and friends, so UploadObject fell outside them
// although the comment justifying the gate named that RPC, and GetDispatcherStats
// would have been swept in by `Get`… no, by nothing — which is the point: a
// name is not a contract. `idempotency_level` is, and 112 of 142 RPCs now
// declare it.
package rpcmeta

import (
	"strings"
	"sync"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"

	// Linked so the registry holds every paladin service whatever the importing
	// binary happens to use. Without these a lookup answers "not found", and
	// every caller below treats that as "I know nothing" rather than guessing —
	// but a silent registry is still a silent guard, so TestEveryRPCResolves
	// fails rather than letting it happen.
	_ "github.com/oleg-tkachuk/paladin/internal/api/pb/admin/v1"
	_ "github.com/oleg-tkachuk/paladin/internal/api/pb/data/v1"
	_ "github.com/oleg-tkachuk/paladin/internal/api/pb/iam/v1"
)

var cache sync.Map // procedure string → result

type result struct {
	level descriptorpb.MethodOptions_IdempotencyLevel
	found bool
}

// Level resolves a Connect procedure ("/pkg.Service/Method") to its declared
// idempotency_level. `found` is false when the procedure names nothing in the
// linked descriptors — which is a different answer from IDEMPOTENCY_UNKNOWN,
// and the callers below depend on the difference.
func Level(procedure string) (descriptorpb.MethodOptions_IdempotencyLevel, bool) {
	if v, ok := cache.Load(procedure); ok {
		r := v.(result)
		return r.level, r.found
	}
	r := lookup(procedure)
	cache.Store(procedure, r)
	return r.level, r.found
}

func lookup(procedure string) result {
	// "/paladin.admin.v1.BucketService/GetBucket" → "paladin.admin.v1.BucketService.GetBucket"
	trimmed := strings.TrimPrefix(procedure, "/")
	slash := strings.LastIndex(trimmed, "/")
	if slash < 0 {
		return result{}
	}
	full := protoreflect.FullName(trimmed[:slash] + "." + trimmed[slash+1:])
	d, err := protoregistry.GlobalFiles.FindDescriptorByName(full)
	if err != nil {
		return result{}
	}
	md, ok := d.(protoreflect.MethodDescriptor)
	if !ok {
		return result{}
	}
	opts, _ := md.Options().(*descriptorpb.MethodOptions)
	return result{level: opts.GetIdempotencyLevel(), found: true}
}

// ForbidsMemoize is the decision, split from the lookup so it can be tested at
// all three levels rather than only at the ones the tree happens to declare.
//
// Only NO_SIDE_EFFECTS forbids memoization. IDEMPOTENT means "repeating is
// safe", a different claim: an idempotent write still writes, and replaying its
// response is legitimate — that is what the key is for. A read's response is a
// snapshot, and handing back an old one answers a question nobody asked.
func ForbidsMemoize(l descriptorpb.MethodOptions_IdempotencyLevel) bool {
	return l == descriptorpb.MethodOptions_NO_SIDE_EFFECTS
}

// IsDeclaredRead reports whether the procedure promises to change nothing.
// A procedure the descriptors do not know claims nothing, so: false.
func IsDeclaredRead(procedure string) bool {
	l, found := Level(procedure)
	return found && ForbidsMemoize(l)
}

// NeedsIdempotencyKey reports whether a client should stamp an
// Idempotency-Key on this call.
//
// Exactly the RPCs that declare IDEMPOTENCY_UNKNOWN, and the reasoning is the
// contract's own. NO_SIDE_EFFECTS changes nothing, so a key buys a row in
// idempotency_keys and a chance of serving a stale snapshot. IDEMPOTENT is
// already safe to repeat — by OCC, by being a removal, or by an upsert to a
// value the caller supplied — so a key again buys a row and no safety. What is
// left is every call whose repeat nobody has been able to promise anything
// about, which is precisely where the key does its work.
//
// An unknown procedure gets no key: stamping a header onto a service this
// binary knows nothing about is not our business, and guessing "probably a
// mutation" is how the prefix lists went wrong in the first place.
func NeedsIdempotencyKey(procedure string) bool {
	l, found := Level(procedure)
	return found && l == descriptorpb.MethodOptions_IDEMPOTENCY_UNKNOWN
}
