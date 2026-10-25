package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/oleg-tkachuk/limes"
	"github.com/oleg-tkachuk/paladin/backend/internal/auth/api_token"
	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/health"
	"github.com/oleg-tkachuk/paladin/backend/internal/store/postgres"
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

// capStore answers IsRevoked with err; nothing else is called.
type capStore struct {
	limes.Store
	err error
}

func (s capStore) IsRevoked(context.Context, uuid.UUID) (bool, error) {
	return false, s.err
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
		// IsRevoked answers an id not on record with false, not ErrNotFound.
		{"capability store answers", &SharedDeps{Capability: &CapabilityBundle{Store: capStore{}}},
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

// lineageStore is a capStore that names no ancestors, as the revocation
// cache, built with RequireLineage, asks of its upstream.
type lineageStore struct{ capStore }

func (lineageStore) RevocationLineage(context.Context, uuid.UUID) (limes.RevocationAnswer, error) {
	return limes.RevocationAnswer{}, nil
}

// The revocations row follows the watcher as the cache hears of it: down
// before its first LISTEN and after a lost connection, which it dates, and
// healthy in between. Never critical: a slower revocation is no outage.
func TestCapabilityRevocationsFollowTheWatcher(t *testing.T) {
	if got := componentOf(t, capabilityRevocationsComponent(&SharedDeps{})); got.Status != health.StatusDisabled {
		t.Fatalf("capability off: component = %+v, want disabled", got)
	}

	cache, err := limes.NewRevocationCache(limes.RevocationCacheConfig{
		Capabilities: lineageStore{}, SyncedTTL: time.Minute, RequireLineage: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	c := capabilityRevocationsComponent(&SharedDeps{Capability: &CapabilityBundle{Revocations: cache}})
	if c.Critical {
		t.Fatal("capability-revocations is critical; a revocation slower to take is no outage")
	}

	got := componentOf(t, c)
	if got.Status != health.StatusUnhealthy || !strings.Contains(got.Message, "not listening yet") {
		t.Errorf("before the first LISTEN: component = %+v, want unhealthy, not listening yet", got)
	}

	signal := cache.NewSignal()
	defer signal.Close()
	signal.Listening()
	signal.Revoked(uuid.New())
	got = componentOf(t, c)
	if got.Status != health.StatusHealthy {
		t.Errorf("listening: component = %+v, want healthy", got)
	}
	want := map[string]string{"connections": "1", "connections lost": "0", "notifications": "1", "narrow clears": "true"}
	for _, d := range got.Details {
		if v, ok := want[d.Name]; ok && v != d.Value {
			t.Errorf("detail %s = %s, want %s", d.Name, d.Value, v)
		}
		delete(want, d.Name)
	}
	if len(want) != 0 {
		t.Errorf("details %v missing from %+v", want, got.Details)
	}

	signal.NotListening()
	got = componentOf(t, c)
	if got.Status != health.StatusUnhealthy || !strings.Contains(got.Message, "lost its connection at") {
		t.Errorf("connection lost: component = %+v, want unhealthy, dated", got)
	}
}
