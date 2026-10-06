// Package cedartest builds a real Cedar engine over a fixed tenant policy, for
// handler tests that must exercise the built-in and tenant policies rather
// than a stub that allows everything. A stub hides exactly the failure that
// matters at this boundary: an action no policy grants.
package cedartest

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/backend/internal/policy/cedar"
)

// cacheTTL is how long the engine keeps a compiled policy set. The policy
// never changes within a test, so any positive value behaves the same.
const cacheTTL = time.Minute

// Store serves Tenant as every tenant's policy and never reports a change.
type Store struct {
	Tenant string
}

// Fetch implements cedar.Store.
func (s Store) Fetch(context.Context, uuid.UUID, string) (cedar.Layers, []byte, string, error) {
	return cedar.Layers{Tenant: s.Tenant}, []byte(s.Tenant), "", nil
}

// Watch implements cedar.Store.
func (Store) Watch(context.Context) (<-chan cedar.ChangeEvent, error) { return nil, nil }

// Engine returns a real engine whose every tenant carries tenantPolicy on top
// of the built-in policy. Empty tenantPolicy leaves the built-in alone.
func Engine(tenantPolicy string) *cedar.Engine {
	return cedar.NewEngine(Store{Tenant: tenantPolicy}, cacheTTL)
}
