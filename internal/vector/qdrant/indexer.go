// Package qdrant is the placeholder Qdrant vector.Indexer. It returns
// vector.ErrNotImplemented from every method and exists only to fix the
// import path now so that flipping the backend later is a config change,
// not a refactor.
//
// When migration triggers fire (>50M vectors per tenant, multi-modal
// collections, payload-filter complexity beyond pgvector HNSW + jsonb),
// replace this stub with the real client (github.com/qdrant/go-client) and
// keep the same package path.
package qdrant

import (
	"context"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/vector"
)

// Config is the wiring shape; intentionally minimal until implemented.
type Config struct {
	Endpoint   string
	APIKey     string
	Collection string
	Dimension  int
}

// Indexer is the not-yet-implemented Qdrant adapter.
type Indexer struct {
	cfg Config
}

// New returns the placeholder. It does not dial Qdrant — the stub is meant
// to be safe to construct in a config that mentions Qdrant ahead of the
// actual implementation landing.
func New(cfg Config) *Indexer { return &Indexer{cfg: cfg} }

// Name implements vector.Indexer.
func (i *Indexer) Name() string { return "qdrant" }

// Upsert implements vector.Indexer.
func (i *Indexer) Upsert(context.Context, []vector.Record) error {
	return vector.ErrNotImplemented
}

// Delete implements vector.Indexer.
func (i *Indexer) Delete(context.Context, []uuid.UUID) error {
	return vector.ErrNotImplemented
}

// DeleteByObject implements vector.Indexer.
func (i *Indexer) DeleteByObject(context.Context, uuid.UUID, string) error {
	return vector.ErrNotImplemented
}

// Search implements vector.Indexer.
func (i *Indexer) Search(context.Context, vector.SearchRequest) ([]vector.Match, error) {
	return nil, vector.ErrNotImplemented
}

// Stats implements vector.Indexer.
func (i *Indexer) Stats(context.Context, uuid.UUID) (vector.Stats, error) {
	return vector.Stats{}, vector.ErrNotImplemented
}
