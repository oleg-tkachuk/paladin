// Package systemh implements the admin/v1 SystemService — operator
// diagnostics that show the running configuration with secrets
// redacted. Distinct from iam/v1.SystemService (open to any
// authenticated caller) because the YAML may shape internal
// architecture an attacker shouldn't see.
package systemh

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"connectrpc.com/connect"
	"gopkg.in/yaml.v3"

	"github.com/oleg-tkachuk/paladin/internal/api/v1/apiutil"
	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/worker"
)

// Handler holds an immutable reference to the loaded config plus the
// on-disk path it came from. Both are set once during process boot —
// SIGHUP-style reload is out of scope, since reloading the config
// safely requires reloading dependent subsystems too.
type Handler struct {
	cfg        config.Config
	sourcePath string
	// httpClient fetches the dispatcher pod's ops endpoint for
	// DispatcherStats. Short timeout — this is a dashboard read.
	httpClient *http.Client
}

// New wires the handler with the live config + the on-disk path it
// was loaded from. Pass an empty path when the process bootstrapped
// from env / flags only — the response renders "—" in that case.
func New(cfg config.Config, sourcePath string) *Handler {
	return &Handler{
		cfg:        cfg,
		sourcePath: sourcePath,
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
}

// MarshalRedacted produces the wire payload: the running config as
// YAML with secrets replaced by Config.Obfuscated()'s "***" sentinels,
// alongside the on-disk path the binary loaded it from.
//
// Gated to platform.admin — this method is the only entry-point the
// connectshim uses, so the role check lives here so the connectshim-
// gating coverage test sees it (rather than tucked into the shim).
func (h *Handler) MarshalRedacted(ctx context.Context) (yamlBlob string, sourcePath string, err error) {
	if rerr := apiutil.RequireRole(ctx, apiutil.RolePlatformAdmin); rerr != nil {
		return "", "", connect.NewError(connect.CodePermissionDenied, rerr)
	}
	redacted := h.cfg.Obfuscated()
	out, err := yaml.Marshal(&redacted)
	if err != nil {
		return "", "", connect.NewError(connect.CodeInternal,
			fmt.Errorf("marshal config: %w", err))
	}
	return string(out), h.sourcePath, nil
}

// DispatcherStats proxies the dispatcher pod's operator view
// (<dispatcher.ops_url>/system/dispatcher-stats.json). The stats are
// computed THERE because event_deliveries is RLS'd per tenant and only the
// dispatcher's BYPASSRLS pool sees the cross-tenant whole; the admin plane
// contributes exactly one thing — this platform.admin gate.
//
// available=false (with a nil stats pointer) when the ops URL is
// unconfigured or unreachable: an operator console renders "stats
// unavailable", it doesn't error — same graceful-degrade contract as
// mcpinspecth.ListSessions.
func (h *Handler) DispatcherStats(ctx context.Context) (stats *worker.DeliveryStats, available bool, err error) {
	if rerr := apiutil.RequireRole(ctx, apiutil.RolePlatformAdmin); rerr != nil {
		return nil, false, connect.NewError(connect.CodePermissionDenied, rerr)
	}
	url := h.cfg.Dispatcher.OpsURL
	if url == "" {
		return nil, false, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url+"/system/dispatcher-stats.json", nil)
	if err != nil {
		return nil, false, connect.NewError(connect.CodeInternal, err)
	}
	res, err := h.httpClient.Do(req)
	if err != nil {
		// Unreachable dispatcher = degraded view, not an RPC failure.
		return nil, false, nil
	}
	defer func() { _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK {
		return nil, false, nil
	}
	var s worker.DeliveryStats
	if err := json.NewDecoder(res.Body).Decode(&s); err != nil {
		return nil, false, connect.NewError(connect.CodeInternal,
			fmt.Errorf("decode dispatcher stats: %w", err))
	}
	return &s, true, nil
}
