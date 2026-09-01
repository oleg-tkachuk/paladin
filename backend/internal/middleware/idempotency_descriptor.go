package middleware

import (
	"strings"
	"sync"

	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"
)

// The server's first use of `option idempotency_level`, read from the
// descriptor rather than guessed from the method name.
//
// Two questions have been conflated here for a while, and this separates them:
//
//   - WHAT AN RPC IS. That is what the option declares, and it is now the
//     source. A name prefix was never able to say it: the rule read
//     Create*/Issue*, so UploadObject fell outside it even though the comment
//     justifying the gate named that RPC.
//
//   - WHICH RPCs THE SERVER REFUSES WITHOUT A KEY. That is policy, and it
//     deliberately stays narrow (Create*/Issue*, see isMutationMethod). Driving
//     enforcement off the descriptor would demand a key on all 106 RPCs nobody
//     has annotated yet, since IDEMPOTENCY_UNKNOWN is the default — breaking
//     every existing client for a classification that is not finished.
//
// So the descriptor's job here is the safe half: never memoize a declared
// read. Replaying one serves a stale answer to a caller who asked for the
// current one, which is worse than not caching at all, and until now a client
// that sent a key on ListBuckets got exactly that.

var idempotencyLevels sync.Map // procedure string → descriptorpb level

// methodIdempotency resolves a Connect procedure ("/pkg.Service/Method") to
// its declared idempotency_level.
//
// Unknown procedures — anything outside the linked descriptors — resolve to
// IDEMPOTENCY_UNKNOWN, which is both the protobuf default and the safe answer:
// it claims nothing, so the caller falls back to today's behaviour rather than
// to a guarantee nobody made.
func methodIdempotency(procedure string) descriptorpb.MethodOptions_IdempotencyLevel {
	if v, ok := idempotencyLevels.Load(procedure); ok {
		return v.(descriptorpb.MethodOptions_IdempotencyLevel)
	}
	level := lookupIdempotency(procedure)
	idempotencyLevels.Store(procedure, level)
	return level
}

func lookupIdempotency(procedure string) descriptorpb.MethodOptions_IdempotencyLevel {
	// "/paladin.admin.v1.BucketService/GetBucket" → "paladin.admin.v1.BucketService.GetBucket"
	trimmed := strings.TrimPrefix(procedure, "/")
	slash := strings.LastIndex(trimmed, "/")
	if slash < 0 {
		return descriptorpb.MethodOptions_IDEMPOTENCY_UNKNOWN
	}
	full := protoreflect.FullName(trimmed[:slash] + "." + trimmed[slash+1:])
	d, err := protoregistry.GlobalFiles.FindDescriptorByName(full)
	if err != nil {
		return descriptorpb.MethodOptions_IDEMPOTENCY_UNKNOWN
	}
	md, ok := d.(protoreflect.MethodDescriptor)
	if !ok {
		return descriptorpb.MethodOptions_IDEMPOTENCY_UNKNOWN
	}
	opts, _ := md.Options().(*descriptorpb.MethodOptions)
	return opts.GetIdempotencyLevel()
}

// levelForbidsMemoize is the decision, split out from the lookup so it can be
// tested at all three levels.
//
// It has to be: only NO_SIDE_EFFECTS is declared anywhere in the tree today, so
// a test that went through the descriptors could not tell this apart from one
// that also treated IDEMPOTENT as a read — a mutation doing exactly that
// survived until this function existed.
//
// Only NO_SIDE_EFFECTS forbids memoization. IDEMPOTENT means "repeating is
// safe", which is a different claim: an idempotent write still writes, and
// replaying its response is legitimate — that is what the key is for. A read's
// response is a snapshot, and handing back an old one is the failure.
func levelForbidsMemoize(l descriptorpb.MethodOptions_IdempotencyLevel) bool {
	return l == descriptorpb.MethodOptions_NO_SIDE_EFFECTS
}

// declaredRead reports whether the procedure promises to change nothing.
func declaredRead(procedure string) bool {
	return levelForbidsMemoize(methodIdempotency(procedure))
}
