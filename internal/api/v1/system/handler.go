// Package system implements SystemService — build/runtime info and
// aggregated health for dashboards. Kubernetes probes use plain HTTP
// handlers registered separately in cmd/server.
package system

import (
	"context"
	"runtime"
	"time"
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

type Handler struct {
	info Info
	db   Pinger
}

func NewHandler(info Info, db Pinger) *Handler {
	if info.GoVersion == "" {
		info.GoVersion = runtime.Version()
	}
	return &Handler{info: info, db: db}
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
