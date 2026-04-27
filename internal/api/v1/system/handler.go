// Package system implements SystemService — build/runtime info and
// aggregated health for dashboards. Kubernetes probes use plain HTTP
// handlers registered separately in cmd/server.
package system

import (
	"context"
	"fmt"
	"runtime"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/oleg-tkachuk/paladin/internal/config"
)

// Pinger is the minimum contract for a component health check.
type Pinger interface {
	Ping(ctx context.Context) error
}

type Info struct {
	Version   string
	Commit    string
	BuildTime time.Time
	GoVersion string
}

type ComponentStatus struct {
	Name      string
	Status    string // OK | DEGRADED | UNHEALTHY
	Message   string
	LatencyMS int64
}

type Health struct {
	Status     string
	Components []ComponentStatus
	CheckedAt  time.Time
}

type ConfigView struct {
	YAML string
	Path string
}

type Handler struct {
	info       Info
	db         Pinger
	cfg        config.Config
	configPath string
}

func NewHandler(info Info, db Pinger, cfg config.Config, configPath string) *Handler {
	if info.GoVersion == "" {
		info.GoVersion = runtime.Version()
	}
	return &Handler{info: info, db: db, cfg: cfg, configPath: configPath}
}

func (h *Handler) GetVersion(_ context.Context) Info {
	return h.info
}

func (h *Handler) GetHealth(ctx context.Context) Health {
	now := time.Now().UTC()
	checks := []ComponentStatus{h.checkDB(ctx)}

	rollup := "OK"
	for _, c := range checks {
		if c.Status == "UNHEALTHY" {
			rollup = "UNHEALTHY"
			break
		}
		if c.Status == "DEGRADED" {
			rollup = "DEGRADED"
		}
	}
	return Health{Status: rollup, Components: checks, CheckedAt: now}
}

// GetConfig returns a YAML serialization of the loaded service configuration
// with secrets (Postgres password, S3 keys, JWT HMAC secret) redacted via
// Config.Obfuscated. Surfaced to the UI's /config page.
func (h *Handler) GetConfig(_ context.Context) (ConfigView, error) {
	safe := h.cfg.Obfuscated()
	out, err := yaml.Marshal(&safe)
	if err != nil {
		return ConfigView{}, fmt.Errorf("marshal config: %w", err)
	}
	return ConfigView{YAML: string(out), Path: h.configPath}, nil
}

func (h *Handler) checkDB(ctx context.Context) ComponentStatus {
	if h.db == nil {
		return ComponentStatus{Name: "postgres", Status: "UNHEALTHY", Message: "not wired"}
	}
	start := time.Now()
	err := h.db.Ping(ctx)
	latency := time.Since(start).Milliseconds()
	if err != nil {
		return ComponentStatus{Name: "postgres", Status: "UNHEALTHY", Message: err.Error(), LatencyMS: latency}
	}
	return ComponentStatus{Name: "postgres", Status: "OK", LatencyMS: latency}
}
