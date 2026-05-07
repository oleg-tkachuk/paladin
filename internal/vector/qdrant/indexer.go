// Package qdrant is the Qdrant-backed vector.Indexer.
//
// We talk REST (POST JSON) rather than gRPC because:
//
//   - Adding the qdrant gRPC client pulls in ~thousands of generated
//     proto types and a heavyweight client transitive graph; PALADIN's
//     binary stays leaner without it.
//
//   - REST is sufficient at PALADIN-scale (low-thousands ops/sec per
//     tenant); the gRPC win is for ML-training workloads in the
//     hundreds-of-thousands ops/sec range.
//
// Multi-tenancy: one collection per PALADIN deployment; isolation is
// enforced by the `tenant_id` payload filter on every query. The
// alternative (one collection per tenant) doesn't scale — Qdrant
// holds an HNSW graph in memory per collection.
//
// Authentication: bearer api_key on every request. Qdrant accepts the
// header `api-key` (lowercase, not `Authorization`); we send both for
// compatibility with proxies that inspect either.
package qdrant

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/oleg-tkachuk/paladin/internal/vector"
)

// Config wires the Indexer.
type Config struct {
	URL        string
	APIKey     string
	Collection string
	Dimension  int
	Timeout    time.Duration
}

// Indexer implements vector.Indexer against a Qdrant REST endpoint.
type Indexer struct {
	cfg  Config
	http *http.Client
}

// New builds an Indexer. Returns an error when wiring is incomplete —
// Empty URL / Collection / Dimension all fail loud at boot.
func New(cfg Config, httpc *http.Client) (*Indexer, error) {
	if cfg.URL == "" {
		return nil, errors.New("qdrant: URL required")
	}
	if cfg.Collection == "" {
		return nil, errors.New("qdrant: Collection required")
	}
	if cfg.Dimension <= 0 {
		return nil, errors.New("qdrant: Dimension must be > 0")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 10 * time.Second
	}
	if httpc == nil {
		httpc = &http.Client{Timeout: cfg.Timeout}
	}
	cfg.URL = strings.TrimRight(cfg.URL, "/")
	return &Indexer{cfg: cfg, http: httpc}, nil
}

// Name implements vector.Indexer.
func (i *Indexer) Name() string { return "qdrant" }

// Upsert implements vector.Indexer. Sends one batch request; Qdrant
// handles arbitrary points/call but very large batches may hit
// payload limits — chunking is the caller's responsibility (a 1000-
// vector batch is comfortable; 10K may need splitting).
func (i *Indexer) Upsert(ctx context.Context, records []vector.Record) error {
	if len(records) == 0 {
		return nil
	}
	type payloadField struct {
		TenantID  string                 `json:"tenant_id"`
		Kind      string                 `json:"kind"`
		ObjectURI string                 `json:"object_uri"`
		ChunkRef  string                 `json:"chunk_ref,omitempty"`
		Model     string                 `json:"model,omitempty"`
		Extra     map[string]interface{} `json:"extra,omitempty"`
	}
	type point struct {
		ID      string       `json:"id"`
		Vector  []float32    `json:"vector"`
		Payload payloadField `json:"payload"`
	}
	type req struct {
		Points []point `json:"points"`
	}
	body := req{Points: make([]point, 0, len(records))}
	for idx, r := range records {
		if r.TenantID == uuid.Nil {
			return fmt.Errorf("qdrant: record[%d] missing tenant_id", idx)
		}
		if len(r.Vector) != i.cfg.Dimension {
			return fmt.Errorf("qdrant: record[%d] dimension %d != configured %d",
				idx, len(r.Vector), i.cfg.Dimension)
		}
		id := r.ID
		if id == uuid.Nil {
			id = uuid.New()
		}
		var extra map[string]interface{}
		if len(r.Payload) > 0 {
			extra = r.Payload
		}
		body.Points = append(body.Points, point{
			ID:     id.String(),
			Vector: r.Vector,
			Payload: payloadField{
				TenantID:  r.TenantID.String(),
				Kind:      string(r.Kind),
				ObjectURI: r.ObjectURI,
				ChunkRef:  r.ChunkRef,
				Model:     r.Model,
				Extra:     extra,
			},
		})
	}
	return i.do(ctx, "PUT", fmt.Sprintf("/collections/%s/points?wait=true", i.cfg.Collection), body, nil)
}

// Delete implements vector.Indexer.
func (i *Indexer) Delete(ctx context.Context, ids []uuid.UUID) error {
	if len(ids) == 0 {
		return nil
	}
	type req struct {
		Points []string `json:"points"`
	}
	body := req{Points: make([]string, len(ids))}
	for k, id := range ids {
		body.Points[k] = id.String()
	}
	return i.do(ctx, "POST", fmt.Sprintf("/collections/%s/points/delete?wait=true", i.cfg.Collection), body, nil)
}

// DeleteByObject implements vector.Indexer.
func (i *Indexer) DeleteByObject(ctx context.Context, tenantID uuid.UUID, objectURI string) error {
	if tenantID == uuid.Nil {
		return vector.ErrTenantRequired
	}
	body := map[string]interface{}{
		"filter": qdrantFilter(tenantID, "", "", objectURI),
	}
	return i.do(ctx, "POST", fmt.Sprintf("/collections/%s/points/delete?wait=true", i.cfg.Collection), body, nil)
}

// Search implements vector.Indexer.
func (i *Indexer) Search(ctx context.Context, sreq vector.SearchRequest) ([]vector.Match, error) {
	if sreq.TenantID == uuid.Nil {
		return nil, vector.ErrTenantRequired
	}
	if len(sreq.Query) != i.cfg.Dimension {
		return nil, fmt.Errorf("qdrant: query dimension %d != configured %d",
			len(sreq.Query), i.cfg.Dimension)
	}
	if sreq.TopK <= 0 {
		sreq.TopK = 10
	}

	body := map[string]interface{}{
		"vector":       sreq.Query,
		"limit":        sreq.TopK,
		"with_payload": true,
		"with_vector":  false,
		"filter":       qdrantFilter(sreq.TenantID, string(sreq.Kind), sreq.EmbeddingModel, ""),
	}
	if sreq.MinScore > 0 {
		body["score_threshold"] = sreq.MinScore
	}

	type rawPoint struct {
		ID      string                 `json:"id"`
		Score   float32                `json:"score"`
		Payload map[string]interface{} `json:"payload"`
	}
	var resp struct {
		Result []rawPoint `json:"result"`
	}
	if err := i.do(ctx, "POST", fmt.Sprintf("/collections/%s/points/search", i.cfg.Collection), body, &resp); err != nil {
		return nil, err
	}

	out := make([]vector.Match, 0, len(resp.Result))
	for _, p := range resp.Result {
		id, err := uuid.Parse(p.ID)
		if err != nil {
			// Qdrant supports integer IDs too; we always emit UUIDs in
			// Upsert, but accept that legacy data might have ints. For
			// now, skip unparseable rows rather than failing the whole
			// search.
			continue
		}
		rec := vector.Record{ID: id}
		if v, ok := p.Payload["tenant_id"].(string); ok {
			if tid, err := uuid.Parse(v); err == nil {
				rec.TenantID = tid
			}
		}
		if v, ok := p.Payload["kind"].(string); ok {
			rec.Kind = vector.Kind(v)
		}
		if v, ok := p.Payload["object_uri"].(string); ok {
			rec.ObjectURI = v
		}
		if v, ok := p.Payload["chunk_ref"].(string); ok {
			rec.ChunkRef = v
		}
		if v, ok := p.Payload["model"].(string); ok {
			rec.Model = v
		}
		if extra, ok := p.Payload["extra"].(map[string]interface{}); ok {
			rec.Payload = extra
		}
		out = append(out, vector.Match{Record: rec, Score: p.Score})
	}
	return out, nil
}

// Stats implements vector.Indexer. Qdrant returns global collection
// stats; per-tenant counts require a count-with-filter call.
func (i *Indexer) Stats(ctx context.Context, tenantID uuid.UUID) (vector.Stats, error) {
	if tenantID == uuid.Nil {
		return vector.Stats{}, vector.ErrTenantRequired
	}
	body := map[string]interface{}{
		"filter": qdrantFilter(tenantID, "", "", ""),
		"exact":  false, // approximate count is fine for dashboards
	}
	var resp struct {
		Result struct {
			Count int64 `json:"count"`
		} `json:"result"`
	}
	if err := i.do(ctx, "POST", fmt.Sprintf("/collections/%s/points/count", i.cfg.Collection), body, &resp); err != nil {
		return vector.Stats{}, err
	}
	return vector.Stats{
		VectorCount: resp.Result.Count,
		ByKind:      map[vector.Kind]int64{},
		ByModel:     map[string]int64{},
	}, nil
}

// qdrantFilter assembles the must-match payload filter Qdrant expects.
// At minimum every query is filtered on tenant_id; optional kind /
// model / object_uri filters are AND-combined.
func qdrantFilter(tenantID uuid.UUID, kind, model, objectURI string) map[string]interface{} {
	must := []map[string]interface{}{
		{"key": "tenant_id", "match": map[string]interface{}{"value": tenantID.String()}},
	}
	if kind != "" {
		must = append(must, map[string]interface{}{
			"key": "kind", "match": map[string]interface{}{"value": kind},
		})
	}
	if model != "" {
		must = append(must, map[string]interface{}{
			"key": "model", "match": map[string]interface{}{"value": model},
		})
	}
	if objectURI != "" {
		must = append(must, map[string]interface{}{
			"key": "object_uri", "match": map[string]interface{}{"value": objectURI},
		})
	}
	return map[string]interface{}{"must": must}
}

// do is the shared JSON HTTP helper. The Qdrant API key goes on the
// `api-key` header (Qdrant convention) AND `Authorization: Bearer …`
// for compatibility with proxies that inspect either.
func (i *Indexer) do(ctx context.Context, method, path string, body, out any) error {
	var rd io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("qdrant: marshal: %w", err)
		}
		rd = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, i.cfg.URL+path, rd)
	if err != nil {
		return fmt.Errorf("qdrant: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if i.cfg.APIKey != "" {
		req.Header.Set("api-key", i.cfg.APIKey)
		req.Header.Set("Authorization", "Bearer "+i.cfg.APIKey)
	}

	resp, err := i.http.Do(req)
	if err != nil {
		return fmt.Errorf("qdrant: %s %s: %w", method, path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("qdrant: %s %s -> %d: %s", method, path, resp.StatusCode, string(b))
	}
	if out == nil {
		return nil
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("qdrant: decode: %w", err)
	}
	return nil
}
