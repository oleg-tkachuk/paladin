// Package litellm is the LiteLLM-proxy implementation of llm.Provider.
//
// LiteLLM exposes an OpenAI-compatible HTTP API in front of every major
// provider (OpenAI, Anthropic, Bedrock, Vertex, Azure, Ollama, vLLM, etc.).
// PALADIN holds no provider keys — only the proxy's master key — and lets the
// proxy do routing, retries, fallbacks, per-virtual-key budgets, and cost
// accounting. We deliberately do not link LiteLLM as a Go library; the
// proxy is its own deployment unit, which keeps PALADIN boot fast and cuts the
// Go module graph by ~thousands of transitive deps.
//
// Endpoint compatibility: LiteLLM serves /v1/chat/completions and
// /v1/embeddings with the OpenAI request schema; we marshal exactly that.
// Cost is read from the X-LiteLLM-Response-Cost response header (proxy-
// specific) when present, falling back to 0 — callers who need exact cost
// must wire LiteLLM with `litellm_params.success_callback` to a database
// and consult that out-of-band.
package litellm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/oleg-tkachuk/paladin/internal/llm"
)

// Config is the wiring shape; populated from internal/config.LLM.
type Config struct {
	// BaseURL of the LiteLLM proxy, e.g. "http://litellm:4000". No trailing slash.
	BaseURL string
	// MasterKey is the single bearer token PALADIN holds; LiteLLM uses it as
	// the gateway credential and routes onward to vendor APIs with its
	// own configured keys. Treat as a secret.
	MasterKey string
	// Timeout caps each request. Embedding batches of hundreds run long;
	// callers chunk above this.
	Timeout time.Duration
}

// Client implements llm.Provider against a LiteLLM proxy.
type Client struct {
	cfg  Config
	http *http.Client
}

// New builds a Client. The provided *http.Client is reused for connection
// pooling; pass nil to use a default with the cfg.Timeout.
func New(cfg Config, httpc *http.Client) (*Client, error) {
	if cfg.BaseURL == "" {
		return nil, errors.New("litellm: BaseURL required")
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Second
	}
	if httpc == nil {
		httpc = &http.Client{Timeout: cfg.Timeout}
	}
	return &Client{cfg: cfg, http: httpc}, nil
}

// Name implements llm.Provider.
func (c *Client) Name() string { return "litellm" }

// chat request / response wire types — narrow OpenAI-compatible subset.
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
	cost, err := c.do(ctx, "/v1/chat/completions", req.CapabilityID, wire, &resp)
	if err != nil {
		return nil, err
	}
	if len(resp.Choices) == 0 {
		return nil, errors.New("litellm: empty choices in chat response")
	}
	return &llm.ChatResponse{
		Content:      resp.Choices[0].Message.Content,
		Model:        resp.Model,
		PromptTokens: resp.Usage.PromptTokens,
		OutputTokens: resp.Usage.CompletionTokens,
		CostUSD:      cost,
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
		PromptTokens int `json:"prompt_tokens"`
		TotalTokens  int `json:"total_tokens"`
	} `json:"usage"`
}

// Embed implements llm.Provider.
func (c *Client) Embed(ctx context.Context, req llm.EmbedRequest) (*llm.EmbedResponse, error) {
	if len(req.Inputs) == 0 {
		return &llm.EmbedResponse{Model: req.Model}, nil
	}
	wire := embedReqWire{Model: req.Model, Input: req.Inputs}
	var resp embedRespWire
	cost, err := c.do(ctx, "/v1/embeddings", req.CapabilityID, wire, &resp)
	if err != nil {
		return nil, err
	}
	// Reorder by Index so callers can rely on Vectors[i] ↔ Inputs[i].
	vectors := make([][]float32, len(req.Inputs))
	dim := 0
	for _, d := range resp.Data {
		if d.Index < 0 || d.Index >= len(vectors) {
			return nil, fmt.Errorf("litellm: out-of-range embedding index %d", d.Index)
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
		CostUSD:    cost,
	}, nil
}

// do is the shared POST-JSON helper. Returns the cost surfaced via the
// proxy's X-LiteLLM-Response-Cost header; 0 when the header is missing.
func (c *Client) do(ctx context.Context, path, capID string, body, out any) (float64, error) {
	buf, err := json.Marshal(body)
	if err != nil {
		return 0, fmt.Errorf("litellm: marshal: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.BaseURL+path, bytes.NewReader(buf))
	if err != nil {
		return 0, fmt.Errorf("litellm: new request: %w", err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.cfg.MasterKey)
	if capID != "" {
		// LiteLLM forwards arbitrary metadata to its callbacks (DB,
		// Prometheus, OpenTelemetry) via this header. Capability ID is
		// the load-bearing key for cost attribution per agent run.
		md, _ := json.Marshal(map[string]string{"capability_id": capID})
		httpReq.Header.Set("X-LiteLLM-Metadata", string(md))
	}

	httpResp, err := c.http.Do(httpReq)
	if err != nil {
		return 0, fmt.Errorf("litellm: request: %w", err)
	}
	defer func() { _ = httpResp.Body.Close() }()

	if httpResp.StatusCode >= 400 {
		// Read body for diagnostics; capped to avoid log bombs.
		b, _ := io.ReadAll(io.LimitReader(httpResp.Body, 4096))
		return 0, fmt.Errorf("litellm: %d: %s", httpResp.StatusCode, string(b))
	}

	if err := json.NewDecoder(httpResp.Body).Decode(out); err != nil {
		return 0, fmt.Errorf("litellm: decode: %w", err)
	}

	cost := 0.0
	if h := httpResp.Header.Get("X-LiteLLM-Response-Cost"); h != "" {
		if v, err := strconv.ParseFloat(h, 64); err == nil {
			cost = v
		}
	}
	return cost, nil
}
