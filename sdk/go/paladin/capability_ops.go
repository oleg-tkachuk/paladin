package paladin

import (
	"fmt"
	"strings"

	// limes defines what an operation's name may be; none of its types cross
	// this package's API.
	"github.com/oleg-tkachuk/limes"
)

// Op is an operation a capability's caveats name. A typed name, so a
// misspelt constant fails to compile rather than reach the server as an
// InvalidArgument.
type Op string

// The operations a capability names in CapabilityCaveats.ops. The server
// imports these, so the two cannot spell them differently, and
// sdk/testdata/capability_ops.json holds both SDKs to the same names.
const (
	// OpGet reads one resource.
	OpGet Op = "get"
	// OpPut writes one resource.
	OpPut Op = "put"
	// OpList lists resources under a prefix.
	OpList Op = "list"
	// OpDelete deletes a resource.
	OpDelete Op = "delete"
	// OpShare delegates a narrower capability through
	// CapabilityService.Delegate. A Biscuit needs none: its holder narrows it
	// offline.
	OpShare Op = "share"
	// OpManage administers: tenants, policy.
	OpManage Op = "manage"
	// OpObjectPresign mints a presigned URL for an object; the URL's own
	// method needs OpGet or OpPut beside it.
	OpObjectPresign Op = "object:presign"
	// OpObjectTag reads or changes an object's tags.
	OpObjectTag Op = "object:tag"
)

// limesErrorPrefix begins limes's errors. Its name is no part of this
// package's, nor of the server's, answers.
const limesErrorPrefix = "limes: "

// Validate refuses a name the server refuses in a caveat, wrapping
// ErrInvalidArgument: one that is neither a built-in operation nor
// namespaced, such as a bare "presign". Its message after
// ErrInvalidArgument's is the server's.
func (op Op) Validate() error {
	if err := limes.Op(op).Validate(); err != nil {
		return fmt.Errorf("%w: %s", ErrInvalidArgument, strings.TrimPrefix(err.Error(), limesErrorPrefix))
	}
	return nil
}

// OpNames is ops as CapabilityCaveats.ops and the rest of the API spell them:
//
//	caveats := &adminv1.CapabilityCaveats{Ops: paladin.OpNames(paladin.OpGet, paladin.OpList)}
func OpNames(ops ...Op) []string {
	names := make([]string, len(ops))
	for i, op := range ops {
		names[i] = string(op)
	}
	return names
}
