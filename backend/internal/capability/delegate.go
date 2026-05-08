package capability

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"
)

// Narrows reports whether `child` is a strict narrowing of `parent`. A
// narrowing means: every operation child allows is allowed by parent;
// every resource child can touch is reachable by parent; the budget /
// request count / TTL are no larger; the audience is a subset; tainted-
// read is no broader. Issuer.Delegate runs this check before signing
// the child token, so a misuse fails at issue time, not later.
//
// The function is exposed at package level so external tooling
// (`paladin cap delegate --check ...`) can dry-run without going through
// the issuer.
func Narrows(parent, child Capability) error {
	// Audience: child audience must be subset of parent audience.
	if !subset(child.Audience, parent.Audience) {
		return fmt.Errorf("%w: audience %v widens parent %v",
			ErrDelegationTooWide, child.Audience, parent.Audience)
	}

	// TTL: child cannot outlive parent. We compare ExpiresAt rather
	// than a derived TTL so capabilities issued back-to-back at the
	// same instant don't drift due to clock skew.
	if child.ExpiresAt.After(parent.ExpiresAt) {
		return fmt.Errorf("%w: child expires_at %s outlives parent %s",
			ErrDelegationTooWide, child.ExpiresAt, parent.ExpiresAt)
	}

	// Tenant: must match. Cross-tenant delegation is impossible — a
	// capability is forever pinned to its issuing tenant.
	if child.Subject.TenantID != parent.Subject.TenantID {
		return fmt.Errorf("%w: tenant_id mismatch", ErrDelegationTooWide)
	}

	if err := narrowsCaveats(parent.Caveats, child.Caveats); err != nil {
		return err
	}
	return nil
}

// narrowsCaveats compares the typed caveat bag. Every restriction in
// the child must be at-least-as-strict as the parent.
func narrowsCaveats(parent, child Caveats) error {
	// Ops: child set ⊆ parent set.
	for _, op := range child.Ops {
		if !slices.Contains(parent.Ops, op) {
			return fmt.Errorf("%w: child op %q not in parent ops %v",
				ErrDelegationTooWide, op, parent.Ops)
		}
	}

	// ResourcePrefixes: every child prefix must be matched (or extended)
	// by some parent prefix. Empty parent = unrestricted, so any child
	// prefix is valid; non-empty parent demands child prefix start with
	// or extend a parent prefix.
	if len(parent.ResourcePrefixes) > 0 {
		if len(child.ResourcePrefixes) == 0 {
			return fmt.Errorf("%w: parent restricts to %v, child unrestricted",
				ErrDelegationTooWide, parent.ResourcePrefixes)
		}
		for _, c := range child.ResourcePrefixes {
			ok := false
			for _, p := range parent.ResourcePrefixes {
				if strings.HasPrefix(c, p) {
					ok = true
					break
				}
			}
			if !ok {
				return fmt.Errorf("%w: child prefix %q not under any parent prefix %v",
					ErrDelegationTooWide, c, parent.ResourcePrefixes)
			}
		}
	}

	// ResourceURIs: every child URI must be in parent URIs or covered
	// by a parent prefix. Empty parent = no exact-URI restriction.
	if len(parent.ResourceURIs) > 0 {
		if len(child.ResourceURIs) == 0 {
			return fmt.Errorf("%w: parent restricts to URIs, child unrestricted",
				ErrDelegationTooWide)
		}
		for _, c := range child.ResourceURIs {
			ok := slices.Contains(parent.ResourceURIs, c)
			if !ok {
				for _, p := range parent.ResourcePrefixes {
					if strings.HasPrefix(c, p) {
						ok = true
						break
					}
				}
			}
			if !ok {
				return fmt.Errorf("%w: child URI %q not in parent allowance",
					ErrDelegationTooWide, c)
			}
		}
	}

	// MaxRequests: 0 = unlimited. Child 0 with parent>0 is invalid
	// (child would have unlimited where parent is bounded).
	if parent.MaxRequests > 0 {
		if child.MaxRequests == 0 || child.MaxRequests > parent.MaxRequests {
			return fmt.Errorf("%w: child requests %d would exceed parent %d",
				ErrDelegationTooWide, child.MaxRequests, parent.MaxRequests)
		}
	}

	// Budget: same rule.
	if parent.MaxBudgetUSD > 0 {
		if child.MaxBudgetUSD == 0 || child.MaxBudgetUSD > parent.MaxBudgetUSD {
			return fmt.Errorf("%w: child budget %.4f exceeds parent %.4f",
				ErrDelegationTooWide, child.MaxBudgetUSD, parent.MaxBudgetUSD)
		}
	}

	// AllowTaintedRead: child cannot grant a privilege the parent lacks.
	if child.AllowTaintedRead && !parent.AllowTaintedRead {
		return fmt.Errorf("%w: child grants tainted_read; parent does not",
			ErrDelegationTooWide)
	}

	// IdempotencyKeyRequired: enforcement-tightening only — child may
	// raise the bar but not lower it. If parent requires key, child
	// must also require key.
	if parent.IdempotencyKeyRequired && !child.IdempotencyKeyRequired {
		return fmt.Errorf("%w: child relaxes idempotency_key requirement",
			ErrDelegationTooWide)
	}

	// SourceIPCIDR: similar — non-empty parent forces child to also
	// constrain. Subset matching of CIDRs is non-trivial; for now we
	// require the slices match exactly so a renderer change on the
	// caller side is detectable. Looser semantics land when an actual
	// caller needs them.
	if len(parent.SourceIPCIDR) > 0 {
		if len(child.SourceIPCIDR) == 0 {
			return errors.New("capability: parent restricts source IP, child unrestricted")
		}
		for _, c := range child.SourceIPCIDR {
			if !slices.Contains(parent.SourceIPCIDR, c) {
				return fmt.Errorf("%w: child CIDR %q not in parent set",
					ErrDelegationTooWide, c)
			}
		}
	}

	return nil
}

// subset reports whether every element of a is in b. Empty a is a
// subset of any b (nothing-claim-anything degenerates safely).
func subset(a, b []string) bool {
	for _, x := range a {
		if !slices.Contains(b, x) {
			return false
		}
	}
	return true
}

// SuggestExpiry caps the supplied desired expiry to `parent.ExpiresAt`.
// Helper for issuer code that wants to default child TTL to "min(req,
// parent expiry)" without re-implementing the logic.
func SuggestExpiry(parent Capability, desired time.Time) time.Time {
	if desired.After(parent.ExpiresAt) {
		return parent.ExpiresAt
	}
	return desired
}
