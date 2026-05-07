package worker

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/llm"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
	"github.com/oleg-tkachuk/paladin/internal/vector"
)

// EmbeddingSource pulls AVAILABLE objects with a non-empty summary that
// haven't been pushed to the Vector Indexer yet.
type EmbeddingSource interface {
	ListObjectsForEmbedding(ctx context.Context, batchSize int32) ([]sqlc.ListObjectsForEmbeddingRow, error)
	MarkObjectEmbedded(ctx context.Context, objectID pgtype.UUID) (int64, error)
}

// EmbeddingWorker is the first caller of the Vector Indexer. It rides
// on the summary text the SummarizationWorker produced — much smaller
// payload than the raw body, so the embedding cost is bounded and the
// search experience covers "find me objects that are about X" without
// chunk-level work.
//
// Future: a chunk-level pipeline for objects above a configurable size
// threshold. Tracked in BACKLOG.
//
// Failure-mode contract mirrors SummarizationWorker:
//   - LLM provider unavailable → quiet skip, leave the row for next tick.
//   - Vector backend rejects → log warn, do NOT mark; we'll retry.
//   - Empty / whitespace-only summary → mark embedded with nothing
//     written to the index. Avoids a permanent retry loop on objects
//     the summarizer chose to skip.
type EmbeddingWorker struct {
	Source    EmbeddingSource
	Indexer   vector.Indexer
	Registry  llm.Registry
	Interval  time.Duration
	BatchSize int32
	Logger    *zap.Logger
}

func (w *EmbeddingWorker) Run(ctx context.Context) error {
	if w.Interval <= 0 {
		w.Interval = 30 * time.Second
	}
	if w.BatchSize <= 0 {
		w.BatchSize = 16
	}
	t := time.NewTicker(w.Interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			w.tick(ctx)
		}
	}
}

func (w *EmbeddingWorker) tick(ctx context.Context) {
	rows, err := w.Source.ListObjectsForEmbedding(ctx, w.BatchSize)
	if err != nil {
		w.log().Warn("list objects for embedding", zap.Error(err))
		return
	}
	for _, row := range rows {
		if ctx.Err() != nil {
			return
		}
		w.embedOne(ctx, row)
	}
}

func (w *EmbeddingWorker) embedOne(ctx context.Context, row sqlc.ListObjectsForEmbeddingRow) {
	logger := w.log().With(
		zap.String("object_id", uuidString(row.ObjectID)),
	)

	summary := ""
	if row.Summary != nil {
		summary = *row.Summary
	}
	if summary == "" {
		// SummarizationWorker stamped an empty placeholder for non-eligible
		// types — mark embedded so it drops out of future sweeps.
		if _, err := w.Source.MarkObjectEmbedded(ctx, row.ObjectID); err != nil {
			logger.Warn("mark embedded (skip)", zap.Error(err))
		}
		return
	}

	provider, model, err := w.Registry.Resolve(llm.RoleEmbeddings)
	if err != nil {
		if errors.Is(err, llm.ErrProviderUnavailable) {
			logger.Debug("embeddings role not configured; skipping")
			return
		}
		logger.Warn("resolve embeddings provider", zap.Error(err))
		return
	}

	resp, err := provider.Embed(ctx, llm.EmbedRequest{
		Model:  model,
		Inputs: []string{summary},
	})
	if err != nil {
		logger.Warn("provider embed", zap.Error(err))
		return
	}
	if resp == nil || len(resp.Vectors) != 1 {
		logger.Warn("provider returned unexpected embedding shape",
			zap.Int("vectors", vectorCount(resp)),
		)
		return
	}

	tenantID := uuidFromPg(row.TenantID)
	rec := vector.Record{
		ID:        uuid.New(),
		TenantID:  tenantID,
		Kind:      vector.KindObject,
		ObjectURI: fmt.Sprintf("object://%s/%s/%s", tenantID, row.BucketName, row.Key),
		Vector:    resp.Vectors[0],
		Model:     resp.Model,
		Payload: map[string]any{
			"object_id":    uuidString(row.ObjectID),
			"object_key":   row.ObjectKey,
			"key":          row.Key,
			"backend_id":   row.BackendID,
			"bucket_name":  row.BucketName,
			"content_type": row.ContentType,
		},
	}
	if err := w.Indexer.Upsert(ctx, []vector.Record{rec}); err != nil {
		logger.Warn("indexer upsert", zap.Error(err))
		return
	}
	if _, err := w.Source.MarkObjectEmbedded(ctx, row.ObjectID); err != nil {
		logger.Warn("mark embedded", zap.Error(err))
		return
	}
	logger.Info("embedded",
		zap.String("model", resp.Model),
		zap.Int("dim", resp.Dimension),
	)
}

func vectorCount(r *llm.EmbedResponse) int {
	if r == nil {
		return 0
	}
	return len(r.Vectors)
}

func (w *EmbeddingWorker) log() *zap.Logger {
	if w.Logger == nil {
		return zap.NewNop()
	}
	return w.Logger
}
