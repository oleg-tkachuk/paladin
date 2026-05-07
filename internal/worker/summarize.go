package worker

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"go.uber.org/zap"

	"github.com/oleg-tkachuk/paladin/internal/llm"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// SummarizationSource is the read side: the worker pulls a small batch
// of AVAILABLE objects without a summary and joins their bucket binding
// inline so it can call S3 in one trip per item.
type SummarizationSource interface {
	ListObjectsForSummary(ctx context.Context, batchSize int32) ([]sqlc.ListObjectsForSummaryRow, error)
	SetObjectSummary(ctx context.Context, summary *string, objectID pgtype.UUID) (int64, error)
}

// ObjectFetcher reads object bytes from the configured S3 backend.
// Matches the signature on s3adapter.Client.Download — kept narrow so
// the worker is testable without a real S3 client.
type ObjectFetcher interface {
	Download(ctx context.Context, bucket string, tenantID uuid.UUID, objectKey, key string, maxBytes int64) ([]byte, error)
}

// SummarizationWorker is the first caller of the LLM Registry. On every
// tick it claims a small batch of unsummarized AVAILABLE objects, pulls
// the bytes, asks the configured RoleSummarize provider to summarize,
// and writes the result back to objects.summary.
//
// Failure-mode contract:
//
//   - LLM provider unavailable / unconfigured → log once at warn, skip the
//     row, leave it for next tick. Never marks the row failed; absence of
//     a summary is non-fatal.
//   - Object body unreadable → log once, skip. The reconciler / lifecycle
//     workers are the right place to act on bad-storage state.
//   - Provider returns a usable response → write and move on.
//
// Day-1 scope: text-shaped content only (allow-list driven by config).
// PDFs, images, and binary blobs route through a future content-extract
// step before they're eligible — tracked in BACKLOG.
type SummarizationWorker struct {
	Source       SummarizationSource
	Fetcher      ObjectFetcher
	Registry     llm.Registry
	Interval     time.Duration
	BatchSize    int32
	MaxBytes     int64
	AllowedTypes map[string]struct{}
	Logger       *zap.Logger
}

// Run blocks until ctx is cancelled. Each tick processes up to BatchSize
// objects; on a backlog the worker is naturally rate-limited by the
// LLM provider's own throughput (the registry returns provider errors
// directly, no retry storm).
func (w *SummarizationWorker) Run(ctx context.Context) error {
	if w.Interval <= 0 {
		w.Interval = 30 * time.Second
	}
	if w.BatchSize <= 0 {
		w.BatchSize = 16
	}
	if w.MaxBytes <= 0 {
		w.MaxBytes = 256 * 1024
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

func (w *SummarizationWorker) tick(ctx context.Context) {
	rows, err := w.Source.ListObjectsForSummary(ctx, w.BatchSize)
	if err != nil {
		w.log().Warn("list objects for summary", zap.Error(err))
		return
	}
	for _, row := range rows {
		if ctx.Err() != nil {
			return
		}
		w.summarizeOne(ctx, row)
	}
}

func (w *SummarizationWorker) summarizeOne(ctx context.Context, row sqlc.ListObjectsForSummaryRow) {
	logger := w.log().With(
		zap.String("object_id", uuidString(row.ObjectID)),
		zap.String("content_type", row.ContentType),
	)

	if !w.contentTypeAllowed(row.ContentType) {
		// Mark with an empty-but-non-NULL placeholder so the row drops out
		// of the next sweep. Empty string means "skipped, not eligible";
		// callers that care about real summaries check for non-empty.
		placeholder := ""
		if _, err := w.Source.SetObjectSummary(ctx, &placeholder, row.ObjectID); err != nil {
			logger.Warn("mark skipped failed", zap.Error(err))
		}
		return
	}

	body, err := w.Fetcher.Download(ctx,
		row.BucketName, uuidFromPg(row.TenantID),
		row.ObjectKey, row.Key, w.MaxBytes,
	)
	if err != nil {
		logger.Warn("download for summary", zap.Error(err))
		return
	}

	provider, model, err := w.Registry.Resolve(llm.RoleSummarize)
	if err != nil {
		if errors.Is(err, llm.ErrProviderUnavailable) {
			// Quiet log — the operator chose not to wire summarize.
			logger.Debug("summarize role not configured; skipping")
			return
		}
		logger.Warn("resolve provider", zap.Error(err))
		return
	}

	resp, err := provider.Chat(ctx, llm.ChatRequest{
		Model: model,
		Messages: []llm.ChatMessage{
			{Role: "system", Content: "Summarise the following content in two sentences. Reply with the summary only — no preamble."},
			{Role: "user", Content: truncateUTF8(string(body), w.MaxBytes)},
		},
		MaxTokens: 256,
	})
	if err != nil {
		logger.Warn("provider chat", zap.Error(err))
		return
	}
	if resp == nil || resp.Content == "" {
		logger.Debug("provider returned empty summary; skipping write")
		return
	}

	summary := resp.Content
	if _, err := w.Source.SetObjectSummary(ctx, &summary, row.ObjectID); err != nil {
		logger.Warn("write summary", zap.Error(err))
		return
	}
	logger.Info("summarized",
		zap.String("model", resp.Model),
		zap.Int("prompt_tokens", resp.PromptTokens),
		zap.Int("output_tokens", resp.OutputTokens),
	)
}

func (w *SummarizationWorker) contentTypeAllowed(ct string) bool {
	if len(w.AllowedTypes) == 0 {
		return false
	}
	_, ok := w.AllowedTypes[ct]
	return ok
}

func (w *SummarizationWorker) log() *zap.Logger {
	if w.Logger == nil {
		return zap.NewNop()
	}
	return w.Logger
}

// truncateUTF8 trims a string to at most n bytes without splitting a
// multi-byte rune. Cheap and good enough for prompt-shaping; we don't
// guarantee a token-aware cap (that's the provider's problem).
func truncateUTF8(s string, n int64) string {
	if n <= 0 || int64(len(s)) <= n {
		return s
	}
	cut := n
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut]
}

// uuidFromPg converts a pgtype.UUID to google/uuid. Returns Nil on an
// invalid pg value — the worker callers tolerate Nil tenant IDs by
// failing the storage call cleanly.
func uuidFromPg(p pgtype.UUID) uuid.UUID {
	if !p.Valid {
		return uuid.Nil
	}
	return uuid.UUID(p.Bytes)
}

// uuidString stringifies a pgtype.UUID for logs without leaking errors.
func uuidString(p pgtype.UUID) string {
	id := uuidFromPg(p)
	if id == uuid.Nil {
		return "<nil>"
	}
	return id.String()
}
