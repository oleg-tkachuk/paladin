// Package vector is PALADIN's pluggable embedding-index abstraction.
//
// The day-1 backend is pgvector — same Postgres PALADIN already runs, no extra
// stateful component, transactional consistency with object metadata.
// Qdrant lands when one of the documented triggers fires (>50M vectors per
// tenant, multi-modal collections with separate dimensions, payload
// filters that pgvector HNSW handles poorly). Until then the Qdrant
// implementation in internal/vector/qdrant returns ErrNotImplemented; the
// interface is fixed now so the swap doesn't ripple through callers.
//
// Filters are NOT free-form. Every Search must be tenant-scoped — that is
// the load-bearing isolation property — and PALADIN enforces it in the
// adapter, not by trusting callers to remember. Additional filter keys
// (kind, run_id, agent_id) are typed; vendor-specific predicates are
// outside the surface on purpose.
package vector

import (
	"context"
	"errors"

	"github.com/google/uuid"
)

// ErrNotImplemented signals a backend that exists in code but is not yet
// usable. The Qdrant adapter returns this from every method until the
// migration trigger fires.
var ErrNotImplemented = errors.New("vector: backend not implemented")

// ErrTenantRequired is returned when an operation lacks a tenant scope.
// Multi-tenant isolation is the whole point — never serve a query without it.
var ErrTenantRequired = errors.New("vector: tenant_id required")

// Kind tracks the PALADIN object kind the vector belongs to. Indexes can be
// partitioned per kind to keep ANN graphs tight and recall high.
type Kind string

const (
	KindObject     Kind = "object"
	KindChunk      Kind = "chunk"
	KindMemory     Kind = "memory"
	KindTranscript Kind = "transcript"
	KindCodeSymbol Kind = "code_symbol"
)

// Record is one indexed vector with its lookup metadata. ID is opaque to
// the caller and assigned by the backend on Upsert; correlate to PALADIN
// objects via ObjectURI.
type Record struct {
	ID        uuid.UUID
	TenantID  uuid.UUID
	Kind      Kind
	ObjectURI string // canonical PALADIN URI: object://tenant/bucket/key
	ChunkRef  string // optional byte-range or chunk locator
	Vector    []float32
	// Payload travels with the record so search can return it without a
	// second metadata round-trip. Keep it small (<2KB); detailed metadata
	// stays in Postgres rows the caller resolves by ObjectURI.
	Payload map[string]any
	Model   string // embedding model that produced Vector — used to gate cross-model searches
}

// SearchRequest narrows the candidate set before ANN. TenantID is required;
// others are optional but typed.
type SearchRequest struct {
	TenantID uuid.UUID
	Kind     Kind // optional: empty matches all kinds
	Query    []float32
	TopK     int
	// MinScore optionally drops weak matches; 0 disables.
	MinScore float32
	// MustMatch is a tiny equality filter on payload fields. Backends
	// translate to native predicates (pgvector → SQL WHERE; Qdrant →
	// filter clauses). Keys not understood by the backend are an error,
	// not silently ignored.
	MustMatch map[string]string
	// EmbeddingModel pins which embeddings index to query. Required when
	// the tenant has multiple models in flight (e.g. mid-migration).
	EmbeddingModel string
}

// Match is one search hit. Score semantics depend on the metric configured
// at index creation (cosine: higher = closer; L2: lower = closer). The
// adapter normalises to "higher is better" so callers don't branch.
type Match struct {
	Record Record
	Score  float32
}

// Indexer is the seam every backend implements. Methods are designed to
// fail closed on missing tenant scope — the production safety property.
type Indexer interface {
	// Name identifies the backend for logs / metrics.
	Name() string

	// Upsert writes records idempotently. Re-upserting the same (TenantID,
	// ObjectURI, ChunkRef, Model) tuple replaces the vector in place.
	Upsert(ctx context.Context, records []Record) error

	// Delete removes vectors by primary key. Used during object
	// soft-delete cascade and tenant offboarding.
	Delete(ctx context.Context, ids []uuid.UUID) error

	// DeleteByObject removes every vector belonging to one ObjectURI
	// (across chunks). Used by the lifecycle reaper.
	DeleteByObject(ctx context.Context, tenantID uuid.UUID, objectURI string) error

	// Search performs ANN with metadata filtering. Returns up to TopK
	// matches; empty slice (not error) when nothing matches.
	Search(ctx context.Context, req SearchRequest) ([]Match, error)

	// Stats returns coarse-grained per-tenant counts. Backends may
	// approximate; expensive exact counts are not required.
	Stats(ctx context.Context, tenantID uuid.UUID) (Stats, error)
}

// Stats is the small status surface admin/observability code reads.
type Stats struct {
	VectorCount int64
	ByKind      map[Kind]int64
	ByModel     map[string]int64
}
