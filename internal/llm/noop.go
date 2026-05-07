package llm

import "context"

// NoopProvider satisfies Provider but always returns ErrProviderUnavailable.
// Used in dev/test stacks and as the safe fallback when the LiteLLM proxy
// is unreachable — keeps callers' error-path code exercised.
type NoopProvider struct{}

func (NoopProvider) Name() string { return "noop" }

func (NoopProvider) Chat(context.Context, ChatRequest) (*ChatResponse, error) {
	return nil, ErrProviderUnavailable
}

func (NoopProvider) Embed(context.Context, EmbedRequest) (*EmbedResponse, error) {
	return nil, ErrProviderUnavailable
}
