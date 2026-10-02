package capability

import (
	"fmt"
	"net/netip"
)

// caveatError is a caveat-violation sentinel that also matches
// ErrCaveatViolation, so a consumer can branch on the specific reason or on
// the family: errors.Is(err, ErrOpNotAllowed) and
// errors.Is(err, ErrCaveatViolation) both hold for an op rejection.
type caveatError struct{ msg string }

func (e *caveatError) Error() string        { return e.msg }
func (e *caveatError) Is(target error) bool { return target == ErrCaveatViolation }

// Request-time caveat rejections. Each also matches ErrCaveatViolation.
var (
	ErrOpNotAllowed           error = &caveatError{"capability: operation not allowed"}
	ErrResourceNotAllowed     error = &caveatError{"capability: resource not allowed"}
	ErrSourceIPNotAllowed     error = &caveatError{"capability: source IP not allowed"}
	ErrIdempotencyKeyRequired error = &caveatError{"capability: idempotency key required"}
	ErrTaintedReadNotAllowed  error = &caveatError{"capability: tainted read not allowed"}
)

// CheckRequest describes one operation a bearer is attempting, in the terms
// the caveats are written in. The consumer fills it from its own request.
type CheckRequest struct {
	// Op is the operation being performed.
	Op Op
	// Resource is the URI the operation touches. Leave it empty only for an
	// operation that is not bound to one resource; a resource-restricted
	// capability refuses such an operation (see Caveats.AllowsResource).
	// For an operation over a set — a listing, a batch — pass the URI or
	// prefix that bounds the whole set.
	Resource string
	// HasIdempotencyKey reports whether the request carried an idempotency
	// key. Consulted only when the caveats require one for a mutating op.
	HasIdempotencyKey bool
	// ResourceTainted reports whether the consumer has flagged the resource
	// with a prompt-injection / PII / secrets signal. A consumer with no
	// taint tracking leaves it false — AllowTaintedRead then protects
	// nothing, which is the consumer's to disclose.
	ResourceTainted bool
}

// Check evaluates every per-operation caveat against req and returns the first
// violation, as one of the sentinels above, or nil. It is the one definition
// of what the caveats mean at use time; a consumer that hand-rolls the same
// checks drifts from it.
//
// The source-IP caveat is per connection rather than per operation, so it is
// checked separately by CheckSource. Budget and request-count caveats are
// stateful and enforced by the UsageStore.
func (c Caveats) Check(req CheckRequest) error {
	if !containsOp(c.Ops, req.Op) {
		return fmt.Errorf("%w: %q not in %v", ErrOpNotAllowed, req.Op, c.Ops)
	}
	if !c.AllowsResource(req.Resource) {
		if req.Resource == "" {
			return fmt.Errorf("%w: operation %q is not bound to a resource, and the capability is resource-restricted",
				ErrResourceNotAllowed, req.Op)
		}
		return fmt.Errorf("%w: %q", ErrResourceNotAllowed, req.Resource)
	}
	if c.IdempotencyKeyRequired && req.Op.Mutating() && !req.HasIdempotencyKey {
		return fmt.Errorf("%w: mutating operation %q", ErrIdempotencyKeyRequired, req.Op)
	}
	if req.ResourceTainted && !c.AllowTaintedRead && !req.Op.Mutating() {
		return fmt.Errorf("%w: %q", ErrTaintedReadNotAllowed, req.Resource)
	}
	return nil
}

// CheckSource evaluates the source-IP caveat against the client address the
// consumer observed. An unknown address (the zero netip.Addr) fails a
// constrained capability closed: "we could not tell" is not "it matched".
func (c Caveats) CheckSource(addr netip.Addr) error {
	if len(c.SourceIPCIDR) == 0 {
		return nil
	}
	if !addr.IsValid() {
		return fmt.Errorf("%w: client address unknown", ErrSourceIPNotAllowed)
	}
	addr = addr.Unmap()
	for _, cidr := range c.SourceIPCIDR {
		p, err := netip.ParsePrefix(cidr)
		if err != nil {
			continue // Validate rejects these at issuance; never a match.
		}
		if p.Contains(addr) {
			return nil
		}
	}
	return fmt.Errorf("%w: %s not in %v", ErrSourceIPNotAllowed, addr, c.SourceIPCIDR)
}

func containsOp(set []Op, want Op) bool {
	for _, op := range set {
		if op == want {
			return true
		}
	}
	return false
}
