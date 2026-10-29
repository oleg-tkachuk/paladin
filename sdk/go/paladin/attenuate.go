package paladin

import (
	"fmt"
	"time"

	// limes writes the Biscuit blocks the server's verifier reads; none of
	// its types cross this package's API.
	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/limes/biscuit"
)

// Attenuation narrows a capability's Biscuit offline. A field left empty
// leaves that dimension as it is; a set one replaces it and must be within
// what the token allows, or the server refuses the whole token.
type Attenuation struct {
	// Ops the copy keeps: OpGet, OpList and the rest of this package's.
	Ops []Op
	// ResourcePrefixes and ResourceURIs the copy keeps; setting either
	// replaces both.
	ResourcePrefixes []string
	ResourceURIs     []string
	// Planes the copy is accepted by: PlaneData and the rest.
	Planes []Plane
	// ExpiresAt is an earlier expiry.
	ExpiresAt time.Time
	// BindJKT binds the copy to a key, by its DPoPThumbprint; only an
	// unbound token can be bound.
	BindJKT string
	// MaxRequests and MaxBudgetNanos give the copy limits of its own,
	// counted apart from other copies and within every limit in force; the
	// budget is in nanos, billionths of the capability's unit. 0 freezes the
	// copy without revoking it; nil sets no limit of its own.
	MaxRequests    *int64
	MaxBudgetNanos *int64
}

// Attenuate is token, a capability's Biscuit, with one more block narrowing
// it by a, as a new token; token keeps working. Nobody, its holder included,
// can remove the block again. A token that is no Paladin Biscuit, or an
// attenuation that names nothing it could narrow to, wraps
// ErrInvalidArgument.
func Attenuate(token string, a Attenuation) (string, error) {
	narrowed, err := biscuit.Attenuate(token, toLimes(a))
	if err != nil {
		return "", fmt.Errorf("%w: attenuate: %w", ErrInvalidArgument, err)
	}
	return narrowed, nil
}

// toLimes is a in limes's own terms.
func toLimes(a Attenuation) limes.Attenuation {
	out := limes.Attenuation{
		ResourcePrefixes: a.ResourcePrefixes,
		ResourceURIs:     a.ResourceURIs,
		ExpiresAt:        a.ExpiresAt,
		ConfirmationJKT:  a.BindJKT,
	}
	if len(a.Planes) > 0 {
		out.Audience = PlaneNames(a.Planes...)
	}
	for _, op := range a.Ops {
		out.Ops = append(out.Ops, limes.Op(op))
	}
	if a.MaxRequests != nil {
		out.Requests = &limes.RequestCeiling{Max: *a.MaxRequests}
	}
	if a.MaxBudgetNanos != nil {
		out.Budget = &limes.BudgetCeiling{Max: limes.Nanos(*a.MaxBudgetNanos)}
	}
	return out
}
