package app

import (
	"context"
	"net/http"

	"github.com/oleg-tkachuk/paladin-private/internal/health"
)

// EmbedMuxes is the bundle returned to in-process callers (the embedded
// MCP transport, future inline tests). Each handler is a raw *http.ServeMux
// — no h2c wrapping, no TCP listener, no graceful-shutdown machinery.
// The caller dispatches to ServeHTTP directly.
//
// Health is the shared shutdown sentinel for the data + iam pair; the
// admin plane carries its own (because admin in production runs in a
// separate Deployment). Embedded callers that want unified shutdown should
// flip both via MarkShuttingDown when their parent context cancels.
type EmbedMuxes struct {
	Data        http.Handler
	IAM         http.Handler
	Admin       http.Handler
	APIHealth   *health.Handler // shared by Data and IAM
	AdminHealth *health.Handler
}

// BuildEmbedMuxes assembles all three plane mux'es for in-process callers.
// Reuses AssembleAPIMuxes and AssembleAdminMux, so a wiring drift in the
// listener path is impossible — any handler change ships to embed mode
// automatically.
func BuildEmbedMuxes(ctx context.Context, deps *SharedDeps, meta BuildMeta) (EmbedMuxes, error) {
	dataMux, iamMux, apiHealth, err := AssembleAPIMuxes(ctx, deps, meta)
	if err != nil {
		return EmbedMuxes{}, err
	}
	adminMux, adminHealth, err := AssembleAdminMux(ctx, deps, meta)
	if err != nil {
		return EmbedMuxes{}, err
	}
	return EmbedMuxes{
		Data:        dataMux,
		IAM:         iamMux,
		Admin:       adminMux,
		APIHealth:   apiHealth,
		AdminHealth: adminHealth,
	}, nil
}
