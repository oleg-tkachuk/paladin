package llm

import (
	"fmt"
	"sync"
)

// StaticRegistry is a config-driven Registry: a fixed Role -> (Provider, model)
// table built at boot and never mutated. Callers that want hot-reload can
// swap the entire registry behind a pointer — the interface is small enough
// that atomic.Value works.
type StaticRegistry struct {
	mu    sync.RWMutex
	table map[Role]binding
}

type binding struct {
	provider Provider
	model    string
}

// NewStaticRegistry builds a Registry from a Role -> (Provider, model) map.
// Bindings with a nil Provider are rejected at build time.
func NewStaticRegistry(bindings map[Role]ProviderBinding) (*StaticRegistry, error) {
	out := &StaticRegistry{table: make(map[Role]binding, len(bindings))}
	for role, b := range bindings {
		if b.Provider == nil {
			return nil, fmt.Errorf("llm: role %q bound to nil provider", role)
		}
		if b.Model == "" {
			return nil, fmt.Errorf("llm: role %q bound to empty model", role)
		}
		out.table[role] = binding{provider: b.Provider, model: b.Model}
	}
	return out, nil
}

// ProviderBinding is the input shape for NewStaticRegistry — keeps the
// constructor signature self-documenting.
type ProviderBinding struct {
	Provider Provider
	Model    string
}

// Resolve implements Registry.
func (r *StaticRegistry) Resolve(role Role) (Provider, string, error) {
	r.mu.RLock()
	b, ok := r.table[role]
	r.mu.RUnlock()
	if !ok {
		return nil, "", ErrProviderUnavailable
	}
	return b.provider, b.model, nil
}
