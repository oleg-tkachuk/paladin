package app

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/vector"
	"github.com/oleg-tkachuk/paladin/internal/vector/pgvector"
	"github.com/oleg-tkachuk/paladin/internal/vector/qdrant"
)

// VectorBundle bundles the configured vector indexer. Lives on
// SharedDeps when the subsystem is enabled; nil otherwise. Callers
// (RAG search, agent memory, on-store embedding pipeline) read
// Bundle.Indexer and degrade gracefully when nil.
type VectorBundle struct {
	// Indexer is the live backend (pgvector or qdrant). Single
	// backend per deployment — multi-backend would require a
	// federation layer above this struct.
	Indexer vector.Indexer

	// BackendName identifies the live backend ("pgvector" or
	// "qdrant"). Surfaced for observability / dashboards / debug
	// logging without poking through the typed Indexer.
	BackendName string
}

// BuildVectorBundle materialises the configured backend. Returns nil
// with no error when cfg.Vector.Enabled is false.
func BuildVectorBundle(cfg config.Vector, deps *SharedDeps) (*VectorBundle, error) {
	if !cfg.Enabled {
		return nil, nil
	}

	switch cfg.Backend {
	case "", "pgvector":
		idx, err := pgvector.New(deps.Pool, pgvector.Config{
			Dimension:    cfg.Pgvector.Dimension,
			DefaultModel: cfg.Pgvector.DefaultModel,
		})
		if err != nil {
			return nil, fmt.Errorf("app: pgvector indexer: %w", err)
		}
		return &VectorBundle{Indexer: idx, BackendName: "pgvector"}, nil

	case "qdrant":
		// Trim potential trailing slash in the URL — same defensive
		// move the Qdrant client makes, but doing it here lets us
		// surface a clear "URL required" error instead of a less
		// obvious "POST /collections//points/upsert 404" later.
		if cfg.Qdrant.URL == "" {
			return nil, errors.New("app: vector.backend=qdrant requires qdrant.url")
		}
		idx, err := qdrant.New(qdrant.Config{
			URL:        cfg.Qdrant.URL,
			APIKey:     cfg.Qdrant.APIKey,
			Collection: cfg.Qdrant.Collection,
			Dimension:  cfg.Qdrant.Dimension,
			Timeout:    cfg.Qdrant.Timeout,
		}, &http.Client{Timeout: cfg.Qdrant.Timeout})
		if err != nil {
			return nil, fmt.Errorf("app: qdrant indexer: %w", err)
		}
		return &VectorBundle{Indexer: idx, BackendName: "qdrant"}, nil

	default:
		return nil, fmt.Errorf("app: unknown vector.backend %q (want pgvector|qdrant)", cfg.Backend)
	}
}
