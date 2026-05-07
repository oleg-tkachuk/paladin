package worker

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/oleg-tkachuk/paladin/internal/llm"
	"github.com/oleg-tkachuk/paladin/internal/store/postgres/sqlc"
)

// fakeSource is an in-memory SummarizationSource. Tracks the
// SetObjectSummary calls so tests can assert on placeholder vs. real.
type fakeSource struct {
	rows     []sqlc.ListObjectsForSummaryRow
	embedded []sqlc.ListObjectsForEmbeddingRow
	wrote    map[string]string // object_id → summary
	marked   map[string]bool   // object_id → embedded
}

func (f *fakeSource) ListObjectsForSummary(_ context.Context, _ int32) ([]sqlc.ListObjectsForSummaryRow, error) {
	return f.rows, nil
}
func (f *fakeSource) SetObjectSummary(_ context.Context, summary *string, id pgtype.UUID) (int64, error) {
	if f.wrote == nil {
		f.wrote = map[string]string{}
	}
	f.wrote[uuidString(id)] = *summary
	return 1, nil
}
func (f *fakeSource) ListObjectsForEmbedding(_ context.Context, _ int32) ([]sqlc.ListObjectsForEmbeddingRow, error) {
	return f.embedded, nil
}
func (f *fakeSource) MarkObjectEmbedded(_ context.Context, id pgtype.UUID) (int64, error) {
	if f.marked == nil {
		f.marked = map[string]bool{}
	}
	f.marked[uuidString(id)] = true
	return 1, nil
}

type fakeFetcher struct{ body []byte }

func (f *fakeFetcher) Download(_ context.Context, _ string, _ uuid.UUID, _, _ string, _ int64) ([]byte, error) {
	return f.body, nil
}

type fakeProvider struct {
	chatResp  *llm.ChatResponse
	embedResp *llm.EmbedResponse
	chatErr   error
	embedErr  error
}

func (p *fakeProvider) Name() string { return "fake" }
func (p *fakeProvider) Chat(_ context.Context, _ llm.ChatRequest) (*llm.ChatResponse, error) {
	return p.chatResp, p.chatErr
}
func (p *fakeProvider) Embed(_ context.Context, _ llm.EmbedRequest) (*llm.EmbedResponse, error) {
	return p.embedResp, p.embedErr
}

type fakeRegistry struct {
	provider llm.Provider
	model    string
	err      error
}

func (r *fakeRegistry) Resolve(_ llm.Role) (llm.Provider, string, error) {
	return r.provider, r.model, r.err
}

func pgUUIDFor(id uuid.UUID) pgtype.UUID {
	return pgtype.UUID{Bytes: id, Valid: true}
}

func TestSummarizationWorker_AllowedContentType(t *testing.T) {
	objID := uuid.New()
	tenantID := uuid.New()
	src := &fakeSource{
		rows: []sqlc.ListObjectsForSummaryRow{{
			ObjectID:    pgUUIDFor(objID),
			TenantID:    pgUUIDFor(tenantID),
			ObjectKey:   "ok",
			Key:         "k",
			ContentType: "text/plain",
			BackendID:   "primary",
			BucketName:  "paladin",
		}},
	}
	w := &SummarizationWorker{
		Source:       src,
		Fetcher:      &fakeFetcher{body: []byte("hello world")},
		Registry:     &fakeRegistry{provider: &fakeProvider{chatResp: &llm.ChatResponse{Content: "summary text", Model: "m"}}, model: "m"},
		MaxBytes:     1024,
		AllowedTypes: map[string]struct{}{"text/plain": {}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	w.tick(ctx)

	got, ok := src.wrote[objID.String()]
	if !ok {
		t.Fatal("expected SetObjectSummary to be called")
	}
	if got != "summary text" {
		t.Errorf("summary = %q, want %q", got, "summary text")
	}
}

func TestSummarizationWorker_DisallowedTypeStampsPlaceholder(t *testing.T) {
	objID := uuid.New()
	src := &fakeSource{
		rows: []sqlc.ListObjectsForSummaryRow{{
			ObjectID:    pgUUIDFor(objID),
			TenantID:    pgUUIDFor(uuid.New()),
			ContentType: "image/png",
		}},
	}
	w := &SummarizationWorker{
		Source:       src,
		Fetcher:      &fakeFetcher{},
		Registry:     &fakeRegistry{},
		AllowedTypes: map[string]struct{}{"text/plain": {}},
	}
	w.tick(context.Background())

	got, ok := src.wrote[objID.String()]
	if !ok {
		t.Fatal("expected placeholder write")
	}
	if got != "" {
		t.Errorf("expected empty placeholder, got %q", got)
	}
}

func TestSummarizationWorker_ProviderUnavailableSkipsQuietly(t *testing.T) {
	objID := uuid.New()
	src := &fakeSource{
		rows: []sqlc.ListObjectsForSummaryRow{{
			ObjectID:    pgUUIDFor(objID),
			TenantID:    pgUUIDFor(uuid.New()),
			ContentType: "text/plain",
		}},
	}
	w := &SummarizationWorker{
		Source:       src,
		Fetcher:      &fakeFetcher{body: []byte("hi")},
		Registry:     &fakeRegistry{err: llm.ErrProviderUnavailable},
		MaxBytes:     16,
		AllowedTypes: map[string]struct{}{"text/plain": {}},
	}
	w.tick(context.Background())

	if _, ok := src.wrote[objID.String()]; ok {
		t.Fatal("ErrProviderUnavailable must not write a summary")
	}
}

func TestSummarizationWorker_ProviderErrorDoesNotMarkRow(t *testing.T) {
	objID := uuid.New()
	src := &fakeSource{
		rows: []sqlc.ListObjectsForSummaryRow{{
			ObjectID:    pgUUIDFor(objID),
			TenantID:    pgUUIDFor(uuid.New()),
			ContentType: "text/plain",
		}},
	}
	w := &SummarizationWorker{
		Source:       src,
		Fetcher:      &fakeFetcher{body: []byte("hi")},
		Registry:     &fakeRegistry{provider: &fakeProvider{chatErr: errors.New("boom")}, model: "m"},
		MaxBytes:     16,
		AllowedTypes: map[string]struct{}{"text/plain": {}},
	}
	w.tick(context.Background())

	if _, ok := src.wrote[objID.String()]; ok {
		t.Fatal("provider error must leave row for retry")
	}
}

func TestTruncateUTF8_DoesNotSplitRune(t *testing.T) {
	got := truncateUTF8("héllo", 2) // 'é' is 2 bytes; cap of 2 should keep just "h"
	if got != "h" {
		t.Errorf("truncateUTF8(%q, 2) = %q, want %q", "héllo", got, "h")
	}
}
