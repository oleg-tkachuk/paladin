package mcp

import (
	"context"
	"sync"
)

// Resource is one MCP read-only data exposure. URI is the canonical address
// (e.g. `paladin://buckets`); Reader returns the content for an exact match.
// Templated URIs (`paladin://policies/{tenant}`) are matched lexically by the
// registry; the implementation receives the rendered URI.
type Resource struct {
	URI         string
	Name        string
	Description string
	MimeType    string
	Reader      ResourceReader
}

type ResourceReader func(ctx context.Context, uri string) (string, error)

type ResourceRegistry struct {
	mu        sync.RWMutex
	resources map[string]Resource
	order     []string
}

func NewResourceRegistry() *ResourceRegistry {
	return &ResourceRegistry{resources: map[string]Resource{}}
}

func (r *ResourceRegistry) Register(res Resource) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.resources[res.URI]; !ok {
		r.order = append(r.order, res.URI)
	}
	r.resources[res.URI] = res
}

func (r *ResourceRegistry) Read(ctx context.Context, uri string) (any, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	res, ok := r.resources[uri]
	if !ok {
		return nil, ErrNotFound
	}
	body, err := res.Reader(ctx, uri)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"contents": []map[string]any{{
			"uri":      uri,
			"mimeType": defaultMime(res.MimeType),
			"text":     body,
		}},
	}, nil
}

func (r *ResourceRegistry) List() []map[string]any {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]map[string]any, 0, len(r.order))
	for _, uri := range r.order {
		res := r.resources[uri]
		out = append(out, map[string]any{
			"uri":         res.URI,
			"name":        res.Name,
			"description": res.Description,
			"mimeType":    defaultMime(res.MimeType),
		})
	}
	return out
}

func defaultMime(m string) string {
	if m == "" {
		return "text/plain"
	}
	return m
}
