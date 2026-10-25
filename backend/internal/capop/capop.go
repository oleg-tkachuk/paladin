// Package capop carries, for one request, the capability operation its
// caveats admitted, from the capability interceptor to the policy engine.
//
// auth.AssertCapabilityOp checks an operation against the capability's
// caveats and stamps it here; the charges ledger reads it to name the op it
// bills, and the Cedar engine exposes it as context.capability_op, so the
// built-in capability permit grants only the actions of an operation the
// caveats already allowed. It lives apart from both because auth imports the
// policy engine and the engine cannot import auth.
//
// The holder is a pointer installed once per request, so a handler stamps it
// without rebinding its context.
package capop

import "context"

type holderKey struct{}

// WithHolder installs a fresh, empty holder. The capability interceptor calls
// it once per request that presents a verified capability.
func WithHolder(ctx context.Context) context.Context {
	return context.WithValue(ctx, holderKey{}, new(string))
}

// Stamp records op as the operation the caveats admitted. The last call in a
// request wins. It does nothing without a holder, so a request that presents
// no capability never carries one.
func Stamp(ctx context.Context, op string) {
	if h, ok := ctx.Value(holderKey{}).(*string); ok && h != nil {
		*h = op
	}
}

// Admitted returns the operation last stamped in this request, or "" when
// none was or no holder is installed.
func Admitted(ctx context.Context) string {
	if h, ok := ctx.Value(holderKey{}).(*string); ok && h != nil {
		return *h
	}
	return ""
}
