// Package llm is PALADIN's pluggable LLM provider abstraction.
//
// PALADIN delegates as much LLM work as possible to the calling MCP host via the
// MCP `sampling` capability — the host already pays for its model and we
// avoid baking provider keys into the control plane. But several flows
// happen out-of-band (background workers, on-store summarization, server-
// side classifiers, embeddings) and need a server-side LLM. This package
// defines the seam those flows speak through.
//
// The intended production binding is the LiteLLM proxy server
// (https://github.com/BerriAI/litellm) — one HTTP endpoint, OpenAI-compatible
// schema, multi-provider routing, retries, per-virtual-key budgets, and a
// cost dashboard outside the PALADIN process. PALADIN holds no provider API keys
// itself; everything goes through the proxy. See internal/llm/litellm for
// the client.
//
// Multiple Providers can coexist (e.g. one for embeddings, one for chat,
// one local-only). The Registry resolves a logical model alias —
// "embeddings.default", "summarize.cheap" — to a concrete Provider+model
// pair so callers stay decoupled from vendor identity.
package llm

import (
	"context"
	"errors"
)

// ErrProviderUnavailable is returned when no provider is configured for the
// requested role. Callers must degrade gracefully — never fail the hot
// path because LLM is missing.
var ErrProviderUnavailable = errors.New("llm: provider unavailable")

// ErrBudgetExceeded is returned when the request would exceed the caller's
// capability budget. Surfaces as a user-visible MCP error (recoverable).
var ErrBudgetExceeded = errors.New("llm: budget exceeded")

// Role is a logical alias the PALADIN code requests; the registry maps it to a
// concrete provider + model. Keeps business code free of vendor names.
type Role string

const (
	RoleEmbeddings        Role = "embeddings.default"
	RoleSummarize         Role = "summarize.cheap"
	RoleClassifyPII       Role = "classify.pii"
	RoleClassifySafety    Role = "classify.safety"
	RoleRerank            Role = "rerank.default"
	RoleStructuredExtract Role = "extract.structured"
)

// ChatMessage is the OpenAI-compat message shape. Every Provider speaks
// this dialect; LiteLLM normalises across vendors so we can too.
type ChatMessage struct {
	Role    string `json:"role"` // system|user|assistant|tool
	Content string `json:"content"`
	Name    string `json:"name,omitempty"`
}

// ChatRequest is the minimum useful surface. JSON-mode is exposed because
// every server-side classifier and structured-extract flow wants it.
type ChatRequest struct {
	Model       string        `json:"model"`
	Messages    []ChatMessage `json:"messages"`
	Temperature float32       `json:"temperature,omitempty"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	JSONMode    bool          `json:"-"` // mapped to response_format=json_object
	// CapabilityID, if set, is forwarded to the proxy as a virtual-key /
	// metadata field so spend gets attributed correctly. LiteLLM honours
	// X-LiteLLM-Metadata for this; the client encodes it.
	CapabilityID string `json:"-"`
}

// ChatResponse is intentionally narrow. Tool calls / streaming are added
// when a real caller needs them — YAGNI default.
type ChatResponse struct {
	Content      string  `json:"content"`
	Model        string  `json:"model"`
	PromptTokens int     `json:"prompt_tokens"`
	OutputTokens int     `json:"output_tokens"`
	CostUSD      float64 `json:"cost_usd"`
}

// EmbedRequest batches inputs into one network call. Most embedding APIs
// support this and the cost saving is large.
type EmbedRequest struct {
	Model        string   `json:"model"`
	Inputs       []string `json:"inputs"`
	CapabilityID string   `json:"-"`
}

// EmbedResponse mirrors EmbedRequest order: Vectors[i] is the embedding
// for Inputs[i]. Dimension is repeated for caller convenience.
type EmbedResponse struct {
	Vectors    [][]float32 `json:"vectors"`
	Dimension  int         `json:"dimension"`
	Model      string      `json:"model"`
	TokensUsed int         `json:"tokens_used"`
	CostUSD    float64     `json:"cost_usd"`
}

// Provider is what every concrete LLM backend implements. Methods may
// return ErrProviderUnavailable to indicate a non-fatal absence — callers
// fall back to heuristics or skip the operation.
type Provider interface {
	// Name identifies the provider for logs / metrics / cost attribution.
	Name() string

	// Chat performs a single completion. Streaming is not modelled here;
	// MCP sampling covers the streaming case and goes through the host.
	Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error)

	// Embed produces dense vectors. Inputs are independent — providers
	// may parallelise internally.
	Embed(ctx context.Context, req EmbedRequest) (*EmbedResponse, error)
}

// Registry resolves a Role to a Provider + concrete model name. Wiring is
// config-driven: a deploy can point RoleEmbeddings at one LiteLLM model
// alias and RoleSummarize at another without code changes.
type Registry interface {
	// Resolve returns the provider + concrete model for the role. Returns
	// ErrProviderUnavailable when no mapping exists; callers must handle.
	Resolve(role Role) (Provider, string, error)
}
