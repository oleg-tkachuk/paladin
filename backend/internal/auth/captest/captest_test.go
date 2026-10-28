package captest

import (
	"testing"

	"github.com/oleg-tkachuk/limes"
)

// A template with only caveats mints a capability that verifies, with the
// defaults filled in and its zero ceilings unlimited.
func TestMintFillsTheDefaults(t *testing.T) {
	v := Verified(t, limes.Capability{Caveats: limes.Caveats{Ops: []limes.Op{limes.OpPut}}})
	c := v.Capability()
	switch {
	case c.Subject.TenantID == [16]byte{}, c.Subject.Type != limes.PrincipalTypeAgent, c.Subject.Subject != Subject:
		t.Errorf("subject = %+v", c.Subject)
	case len(c.Audience) != 1 || c.Audience[0] != Audience:
		t.Errorf("audience = %v", c.Audience)
	case !c.Caveats.Requests.Unlimited || !c.Caveats.Budget.Unlimited:
		t.Errorf("ceilings = %s, %s; want unlimited", c.Caveats.Requests, c.Caveats.Budget)
	case len(c.Caveats.Ops) != 1 || c.Caveats.Ops[0] != limes.OpPut:
		t.Errorf("ops = %v", c.Caveats.Ops)
	}
	if v.MeterSubject().CapabilityID() != c.ID {
		t.Error("the meter subject names another capability")
	}
}

// MintExact keeps a ceiling of nothing, which Mint would lift.
func TestMintExactKeepsAZeroCeiling(t *testing.T) {
	v, _ := NewMinter(t).MintExact(t, limes.Capability{Caveats: limes.Caveats{
		Requests: limes.RequestCeiling{Unlimited: true}, Budget: limes.BudgetCeiling{},
	}})
	if got := v.Capability().Caveats.Budget; got != (limes.BudgetCeiling{}) {
		t.Errorf("budget = %s, want 0", got)
	}
}

// A delegated child and an attenuated copy verify, and say what they are.
func TestDelegateAndAttenuate(t *testing.T) {
	m := NewMinter(t)
	parent, _ := m.Mint(t, limes.Capability{Caveats: limes.Caveats{Ops: []limes.Op{limes.OpGet, limes.OpShare}}})
	child, _ := m.Delegate(t, parent, limes.Caveats{
		Ops:      []limes.Op{limes.OpGet},
		Requests: limes.RequestCeiling{Max: 1}, Budget: limes.BudgetCeiling{Max: 1},
	})
	if child.Capability().ParentID != parent.Capability().ID {
		t.Errorf("child parent = %s, want %s", child.Capability().ParentID, parent.Capability().ID)
	}
	budget := limes.BudgetCeiling{Max: 1}
	copied, _ := m.Attenuated(t, parent, limes.Attenuation{Budget: &budget})
	if !copied.FromBiscuit() || len(copied.MeterSubject().Copies()) != 1 {
		t.Errorf("copy: from biscuit %t, %d copies", copied.FromBiscuit(), len(copied.MeterSubject().Copies()))
	}
}
