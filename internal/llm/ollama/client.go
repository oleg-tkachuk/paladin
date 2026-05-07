// Package ollama is the Ollama-local-inference implementation of
// llm.Provider.
//
// Ollama exposes an OpenAI-compatible API at /v1/chat/completions and
// /v1/embeddings (in recent versions; native API at /api/* is the
// historical path but `/v1/*` is what we target for parity with the
// LiteLLM client). Local inference means: no auth, no usage-based
// billing, cost is always zero. Useful for:
//
//   - air-gapped deploys where outbound API calls are forbidden;
//   - embedding workloads at scale where vendor pricing is prohibitive;
//   - dev mode without an internet round-trip per call.
//
// Distinct from internal/llm/litellm because:
//
//   - no Authorization header (no proxy bearer key);
//   - no LiteLLM-specific cost-attribution headers;
//   - wider default timeout (local CPU inference is slow on cold start).
//
// Wire format is OpenAI-compat. We deliberately don't reuse the
// LiteLLM client by parameterising the auth header — keeping these as
// two narrow clients makes the production-vs-dev distinction load-
// bearing in code, not in config.
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/llm"
)

// Config wires a Client; populated from internal/config.LLMOllama.
type Config struct {
	// BaseURL of the Ollama daemon (e.g. http://ollama:11434).
	// No trailing slash; the client appends /v1/...
	BaseURL string
	// Timeout caps each request. Default 2 minutes when zero — local
	// inference's cold-start latency on a fresh model can be ~30s plus
	// generation time.
	Timeout time.Duration
}

// Client implements llm.Provider against an Ollama daemon.
type Client struct {
	cfg  Config
	http *http.Client
}

// New builds a Client. The provided *http.Client is reused for
// connection pooling; pass nil to use a default with the cfg.Timeout.
func New(cfg Config, httpc *http.Client) (*Client, error) {
	if cfg.BaseURL == "" {
		return nil, errors.New("ollama: BaseURL required")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 2 * time.Minute
	}
	if httpc == nil {
		httpc = &http.Client{Timeout: cfg.Timeout}
	}
	return &Client{cfg: cfg, http: httpc}, nil
}

// Name implements llm.Provider.
func (c *Client) Name() string { return "ollama" }

// chatReqWire / chatRespWire are the OpenAI-compat shapes Ollama
// implements. ResponseFormat is supported in recent Ollama builds for
// structured output; we expose it via the JSONMode flag on
// llm.ChatRequest.
type chatReqWire struct {
	Model          string            `json:"model"`
	Messages       []llm.ChatMessage `json:"messages"`
	Temperature    float32           `json:"temperature,omitempty"`
	MaxTokens      int               `json:"max_tokens,omitempty"`
	ResponseFormat *responseFormat   `json:"response_format,omitempty"`
}

type responseFormat struct {
	Type string `json:"type"`
}

type chatRespWire struct {
	Model   string `json:"model"`
	Choices []struct {
		Message llm.ChatMessage `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

// Chat implements llm.Provider.
func (c *Client) Chat(ctx context.Context, req llm.ChatRequest) (*llm.ChatResponse, error) {
	wire := chatReqWire{
		Model:       req.Model,
		Messages:    req.Messages,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
	}
	if req.JSONMode {
		wire.ResponseFormat = &responseFormat{Type: "json_object"}
	}
	var resp chatRespWire
	if err := c.do(ctx, "/v1/chat/completions", wire, &resp); err != nil {
		return nil, err
	}
	if len(resp.Choices) == 0 {
		return nil, errors.New("ollama: empty choices in chat response")
	}
	return &llm.ChatResponse{
		Content:      resp.Choices[0].Message.Content,
		Model:        resp.Model,
		PromptTokens: resp.Usage.PromptTokens,
		OutputTokens: resp.Usage.CompletionTokens,
		// CostUSD is always 0 for Ollama — local inference is free at
		// the API boundary; the only real cost is power, which we
		// don't track.
	}, nil
}

type embedReqWire struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embedRespWire struct {
	Model string `json:"model"`
	Data  []struct {
		Embedding []float32 `json:"embedding"`
		Index     int       `json:"index"`
	} `json:"data"`
	Usage struct {
		TotalTokens int `json:"total_tokens"`
	} `json:"usage"`
}

// Embed implements llm.Provider.
func (c *Client) Embed(ctx context.Context, req llm.EmbedRequest) (*llm.EmbedResponse, error) {
	if len(req.Inputs) == 0 {
		return &llm.EmbedResponse{Model: req.Model}, nil
	}
	wire := embedReqWire{Model: req.Model, Input: req.Inputs}
	var resp embedRespWire
	if err := c.do(ctx, "/v1/embeddings", wire, &resp); err != nil {
		return nil, err
	}
	vectors := make([][]float32, len(req.Inputs))
	dim := 0
	for _, d := range resp.Data {
		if d.Index < 0 || d.Index >= len(vectors) {
			return nil, fmt.Errorf("ollama: out-of-range embedding index %d", d.Index)
		}
		vectors[d.Index] = d.Embedding
		if dim == 0 {
			dim = len(d.Embedding)
		}
	}
	return &llm.EmbedResponse{
		Vectors:    vectors,
		Dimension:  dim,
		Model:      resp.Model,
		TokensUsed: resp.Usage.TotalTokens,
	}, nil
}

// do is the shared POST-JSON helper. No auth header — Ollama accepts
// unauthenticated calls; the network policy / NetworkPolicy is the
// access boundary.
func (c *Client) do(ctx context.Context, path string, body, out any) error {
	buf, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("ollama: marshal: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+path, bytes.NewReader(buf))
	if err != nil {
		return fmt.Errorf("ollama: new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")

	httpResp, err := c.http.Do(httpReq)
	if err != nil {
		return fmt.Errorf("ollama: request: %w", err)
	}
	defer func() { _ = httpResp.Body.Close() }()

	if httpResp.StatusCode >= 400 {
		b, _ := io.ReadAll(io.LimitReader(httpResp.Body, 4096))
		return fmt.Errorf("ollama: %d: %s", httpResp.StatusCode, string(b))
	}
	if err := json.NewDecoder(httpResp.Body).Decode(out); err != nil {
		return fmt.Errorf("ollama: decode: %w", err)
	}
	return nil
}
