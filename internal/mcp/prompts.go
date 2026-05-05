package mcp

import (
	"context"
	"sync"
)

// Prompt is a reusable prompt template surfaced to the LLM so it can pick
// from canned, parameterised workflows ("audit access for tenant X",
// "rotate credentials for backend Y", etc.).
type Prompt struct {
	Name        string
	Description string
	Arguments   []PromptArgument
	Renderer    PromptRenderer
}

type PromptArgument struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Required    bool   `json:"required"`
}

// PromptRenderer produces the MCP `messages` array. Each message has a
// `role` and `content` (text). Arguments are pre-validated by the registry.
type PromptRenderer func(ctx context.Context, args map[string]string) ([]PromptMessage, error)

type PromptMessage struct {
	Role    string `json:"role"`    // "user" | "assistant"
	Content string `json:"content"` // plain text
}

type PromptRegistry struct {
	mu      sync.RWMutex
	prompts map[string]Prompt
	order   []string
}

func NewPromptRegistry() *PromptRegistry {
	return &PromptRegistry{prompts: map[string]Prompt{}}
}

func (r *PromptRegistry) Register(p Prompt) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.prompts[p.Name]; !ok {
		r.order = append(r.order, p.Name)
	}
	r.prompts[p.Name] = p
}

func (r *PromptRegistry) List() []map[string]any {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]map[string]any, 0, len(r.order))
	for _, name := range r.order {
		p := r.prompts[name]
		out = append(out, map[string]any{
			"name":        p.Name,
			"description": p.Description,
			"arguments":   p.Arguments,
		})
	}
	return out
}

func (r *PromptRegistry) Render(ctx context.Context, name string, args map[string]string) (any, error) {
	r.mu.RLock()
	p, ok := r.prompts[name]
	r.mu.RUnlock()
	if !ok {
		return nil, ErrNotFound
	}
	msgs, err := p.Renderer(ctx, args)
	if err != nil {
		return nil, err
	}
	out := make([]map[string]any, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, map[string]any{
			"role": m.Role,
			"content": map[string]any{
				"type": "text",
				"text": m.Content,
			},
		})
	}
	return map[string]any{"messages": out}, nil
}
