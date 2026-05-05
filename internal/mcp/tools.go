package mcp

import (
	"context"
	"encoding/json"
	"sync"
)

// Tool is one MCP-exposed capability. Handler receives the raw JSON
// `arguments` payload and returns a content envelope per MCP spec.
type Tool struct {
	Name        string
	Description string
	// InputSchema is JSON Schema for the `arguments` parameter. Surfaced to
	// the LLM so it can synthesize valid calls.
	InputSchema map[string]any
	Handler     ToolHandler
}

type ToolHandler func(ctx context.Context, args json.RawMessage) (any, error)

// ToolRegistry is a thread-safe map of registered Tools.
type ToolRegistry struct {
	mu    sync.RWMutex
	tools map[string]Tool
	order []string
}

func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{tools: map[string]Tool{}}
}

func (r *ToolRegistry) Register(t Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.tools[t.Name]; !ok {
		r.order = append(r.order, t.Name)
	}
	r.tools[t.Name] = t
}

func (r *ToolRegistry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// List returns the tools in registration order. Each entry is the public
// MCP-spec shape: name + description + inputSchema.
func (r *ToolRegistry) List() []map[string]any {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]map[string]any, 0, len(r.order))
	for _, name := range r.order {
		t := r.tools[name]
		out = append(out, map[string]any{
			"name":        t.Name,
			"description": t.Description,
			"inputSchema": t.InputSchema,
		})
	}
	return out
}

// TextResult builds a one-text-block tool response in MCP shape. Used by
// tool handlers that return JSON strings.
func TextResult(text string) any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
	}
}

// JSONResult marshals v and returns it as a single text content block.
func JSONResult(v any) (any, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return TextResult(string(b)), nil
}
