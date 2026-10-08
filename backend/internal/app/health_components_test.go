package app

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/paladin/backend/internal/auth/api_token"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/health"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres"
	"github.com/oleg-tkachuk/paladin/capability"
)

// componentOf runs p as the health page would.
func componentOf(t *testing.T, p health.Probe) health.Component {
	t.Helper()
	return (&health.Handler{Ready: []health.Probe{p}}).Snapshot(context.Background(), "api").Components[0]
}

// With no replica the health page still shows the row, as off by its config
// key: there is nothing to be healthy or not, so it is not run.
func TestReplicaCheckDisabledWithoutReplica(t *testing.T) {
	db := &postgres.DB{Pool: &pgxpool.Pool{}, Reads: postgres.NewPrimaryOnlyRouter(&pgxpool.Pool{})}
	c := replicaCheck(db)
	if c.Name != "postgres-replica" || c.Category != health.CategoryDatabase || c.Critical {
		t.Fatalf("check = %+v; want a non-critical database row named postgres-replica", c)
	}
	got := componentOf(t, c)
	if got.Status != health.StatusDisabled || got.Control != health.ControlConfig ||
		got.Message != "off by configuration: "+config.KeyReplicaEnabled {
		t.Errorf("component = %+v, want off by %s", got, config.KeyReplicaEnabled)
	}
}

// capStore answers Get with err; nothing else is called.
type capStore struct {
	capability.Store
	err error
}

func (s capStore) Get(context.Context, uuid.UUID) (capability.Capability, error) {
	return capability.Capability{}, s.err
}

// tokenStore answers FindByDigest with err; nothing else is called.
type tokenStore struct {
	api_token.Store
	err error
}

func (s tokenStore) FindByDigest(context.Context, []byte) (api_token.Token, error) {
	return api_token.Token{}, s.err
}

// capability and api_token were reported healthy by checks that returned
// nil unconditionally. On, each now reads its store as the request path does.
func TestSubsystemComponentsCheckTheirStore(t *testing.T) {
	denied := errors.New("permission denied for table")
	cases := []struct {
		name string
		deps *SharedDeps
		key  string
		want health.ComponentStatus
	}{
		{"capability off", &SharedDeps{}, config.KeyCapabilityEnabled, health.StatusDisabled},
		{"capability store answers", &SharedDeps{Capability: &CapabilityBundle{Store: capStore{err: capability.ErrNotFound}}},
			config.KeyCapabilityEnabled, health.StatusHealthy},
		{"capability store fails", &SharedDeps{Capability: &CapabilityBundle{Store: capStore{err: denied}}},
			config.KeyCapabilityEnabled, health.StatusUnhealthy},
		{"api_token off", &SharedDeps{}, config.KeyAPITokenEnabled, health.StatusDisabled},
		{"api_token store answers", &SharedDeps{APIToken: &APITokenBundle{Store: tokenStore{err: api_token.ErrTokenNotFound}}},
			config.KeyAPITokenEnabled, health.StatusHealthy},
		{"api_token store fails", &SharedDeps{APIToken: &APITokenBundle{Store: tokenStore{err: denied}}},
			config.KeyAPITokenEnabled, health.StatusUnhealthy},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := capabilityComponent(tc.deps)
			if tc.key == config.KeyAPITokenEnabled {
				p = apiTokenComponent(tc.deps)
			}
			got := componentOf(t, p)
			if got.Status != tc.want || got.Control != health.ControlConfig {
				t.Errorf("component = %+v, want %s, switched by config", got, tc.want)
			}
			if tc.want == health.StatusDisabled && got.Message != "off by configuration: "+tc.key {
				t.Errorf("message = %q, want it to name %s", got.Message, tc.key)
			}
		})
	}
}
