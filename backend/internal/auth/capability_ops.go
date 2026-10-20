package auth

import "github.com/oleg-tkachuk/limes"

// The data plane's own capability operations. limes defines only the ones
// every resource server shares (get, put, list, delete, share, manage); a
// consumer's are namespaced, and their effect is the consumer's to declare.
const (
	// OpPresign mints a presigned URL for an object. The URL's own method is
	// asserted beside it (OpGet or OpPut), so presign itself only reads.
	OpPresign limes.Op = "object:presign"
	// OpTag reads or changes an object's tags; left undeclared, it is held
	// to the caveats that tighten writes.
	OpTag limes.Op = "object:tag"
)

// opEffects is the effect declared for each of the data plane's own
// operations; one absent here is a write (limes.Op.ResolveEffect).
var opEffects = map[limes.Op]limes.Effect{
	OpPresign: limes.EffectRead,
}
