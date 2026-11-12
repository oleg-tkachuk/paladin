package systemh

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/backend/internal/config"
	"github.com/oleg-tkachuk/paladin/backend/internal/health"
	"github.com/oleg-tkachuk/paladin/backend/internal/logfield"
)

// reachabilityComponent names the one component of a role this pod could
// not read: the role itself is down, or its snapshot is refused.
const reachabilityComponent = "reachability"

// reachabilityCategory groups it with the other cross-pod checks.
const reachabilityCategory = "upstream"

// PlatformHealth reads every configured role's snapshot at once, each
// bounded by admin.health.timeout, and returns them in the configured order.
// A role that does not answer, or answers with anything but its snapshot, is
// listed as unhealthy with a reachability component that says why.
func (h *Handler) PlatformHealth(ctx context.Context) []health.Snapshot {
	roles := h.cfg.Admin.Health.Roles
	out := make([]health.Snapshot, len(roles))
	var wg sync.WaitGroup
	for i, r := range roles {
		wg.Go(func() {
			snap, err := h.fetchSnapshot(ctx, r)
			if err != nil {
				h.log.Warn("platform health: role unreachable",
					zap.String("role", r.Name), logfield.URL("url", r.URL), zap.Error(err))
				snap = unreachableSnapshot(r.Name, err)
			}
			out[i] = snap
		})
	}
	wg.Wait()
	return out
}

func (h *Handler) fetchSnapshot(ctx context.Context, r config.AdminHealthRole) (health.Snapshot, error) {
	ctx, cancel := context.WithTimeout(ctx, h.cfg.Admin.Health.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.URL+health.SnapshotPath, nil)
	if err != nil {
		return health.Snapshot{}, err
	}
	if token := h.cfg.Runtime.HealthSnapshotToken; token != "" {
		req.Header.Set(health.SnapshotTokenHeader, token)
	}
	client := h.healthClient
	if client == nil {
		client = h.httpClient
	}
	res, err := client.Do(req)
	if err != nil {
		return health.Snapshot{}, err
	}
	defer func() { _ = res.Body.Close() }()
	// A draining role answers 503 with its snapshot still in the body.
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusServiceUnavailable {
		return health.Snapshot{}, fmt.Errorf("answered %d", res.StatusCode)
	}
	var snap health.Snapshot
	if err := json.NewDecoder(res.Body).Decode(&snap); err != nil {
		return health.Snapshot{}, fmt.Errorf("unreadable snapshot: %w", err)
	}
	if snap.Role == "" {
		snap.Role = r.Name
	}
	return snap, nil
}

func unreachableSnapshot(role string, err error) health.Snapshot {
	return health.Snapshot{
		Role:   role,
		Status: health.StatusUnhealthy,
		Components: []health.Component{{
			Name:     reachabilityComponent,
			Status:   health.StatusUnhealthy,
			Message:  "unreachable: " + err.Error(),
			Category: reachabilityCategory,
			Critical: true,
			Control:  health.ControlAlwaysOn,
		}},
		CheckedAt: time.Now(),
	}
}
