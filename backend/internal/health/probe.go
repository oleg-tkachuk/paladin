package health

import "context"

// Probe is the contract every component a role reports on implements: what
// it is, whether it is on, and how to check it. The handler reads the switch
// first and checks only a component that is on — one that is off is listed
// as disabled, with why, and its dependency is never touched.
type Probe interface {
	// Spec is the component's identity. It does not change at runtime.
	Spec() Spec
	// Enablement reads the component's switch. It may read configuration
	// or the database, never the dependency the component stands for. An
	// error is the component's failure: whether it is on is not known.
	Enablement(ctx context.Context) (Enablement, error)
	// Check exercises the dependency. Called only while it is enabled.
	Check(ctx context.Context) error
}

// Spec names a component and says how much it matters.
type Spec struct {
	Name     string
	Category Category
	// Critical: a failure takes the pod out of service (/readyz 503).
	// Otherwise it degrades the role and the pod keeps serving.
	Critical bool
}

// Control is where a component's switch lives.
type Control string

const (
	// ControlAlwaysOn has no switch: the role cannot run without it.
	ControlAlwaysOn Control = "always_on"
	// ControlConfig is switched by a configuration key, read at startup.
	ControlConfig Control = "config"
	// ControlDatabase is switched by rows in the database: the component
	// is in use while something stored there uses it.
	ControlDatabase Control = "database"
)

// Enablement is a component's switch as read now.
type Enablement struct {
	Control Control
	Enabled bool
	// Reason says why a disabled component is off; shown as its message.
	Reason string
}

// AlwaysOn is the switch of a component the role cannot run without.
func AlwaysOn() Enablement {
	return Enablement{Control: ControlAlwaysOn, Enabled: true}
}

// ByConfig is a switch held by the configuration key key.
func ByConfig(enabled bool, key string) Enablement {
	e := Enablement{Control: ControlConfig, Enabled: enabled}
	if !enabled {
		e.Reason = "off by configuration: " + key
	}
	return e
}

// ByDatabase is a switch held by stored rows; reason says, when nothing
// uses the component, what would.
func ByDatabase(inUse bool, reason string) Enablement {
	e := Enablement{Control: ControlDatabase, Enabled: inUse}
	if !inUse {
		e.Reason = "not in use: " + reason
	}
	return e
}

// Check is the standard Probe: a Spec, a switch and a check function.
type Check struct {
	Name     string
	Category Category
	Critical bool
	// Switch reads whether the component is on; nil is AlwaysOn.
	Switch func(context.Context) (Enablement, error)
	// Func exercises the dependency. Unused while the switch is off, so a
	// component that is off may leave it nil.
	Func func(context.Context) error
}

// Fixed is a switch decided once, at startup — a configuration key's.
func Fixed(e Enablement) func(context.Context) (Enablement, error) {
	return func(context.Context) (Enablement, error) { return e, nil }
}

// Spec implements Probe.
func (c Check) Spec() Spec {
	return Spec{Name: c.Name, Category: c.Category, Critical: c.Critical}
}

// Enablement implements Probe.
func (c Check) Enablement(ctx context.Context) (Enablement, error) {
	if c.Switch == nil {
		return AlwaysOn(), nil
	}
	return c.Switch(ctx)
}

// Check implements Probe.
func (c Check) Check(ctx context.Context) error { return c.Func(ctx) }
