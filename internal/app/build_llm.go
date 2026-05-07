package app

import (
	"errors"
	"fmt"
	"net/http"

	"github.com/oleg-tkachuk/paladin/internal/config"
	"github.com/oleg-tkachuk/paladin/internal/llm"
	"github.com/oleg-tkachuk/paladin/internal/llm/litellm"
	"github.com/oleg-tkachuk/paladin/internal/llm/ollama"
)

// LLMBundle bundles the configured providers + the Registry that maps
// Roles to (Provider, model). Lives on SharedDeps when the subsystem
// is enabled; nil otherwise. Callers (workers, MCP fallbacks) read
// Registry.Resolve(role) and degrade gracefully on
// ErrProviderUnavailable.
type LLMBundle struct {
	Registry llm.Registry

	// Providers is the resolved provider table, keyed by name
	// ("litellm" / "ollama"). Surfaced for code that wants to invoke
	// a specific provider directly (e.g. a self-hosted-only path
	// that bypasses Bindings).
	Providers map[string]llm.Provider
}

// BuildLLMBundle materialises providers from cfg.LLM and constructs a
// StaticRegistry from cfg.LLM.Bindings. Returns nil with no error when
// the subsystem is disabled.
//
// Provider construction is lenient: if a provider's BaseURL is empty,
// it's not built and Bindings that reference it fail to resolve at
// runtime — caller-side ErrProviderUnavailable handling kicks in. This
// matches the YAML-flexibility expectation: an operator can roll out
// Ollama-only deploys without wiring LiteLLM, and vice versa.
func BuildLLMBundle(cfg config.LLM, _ *SharedDeps) (*LLMBundle, error) {
	if !cfg.Enabled {
		return nil, nil
	}

	providers := map[string]llm.Provider{}

	if cfg.LiteLLM.BaseURL != "" {
		// Master key is resolved by K8sSecretResolver upstream; the
		// inline APIKey field carries the resolved secret by the time
		// we read it. Tests / dev set APIKey directly.
		c, err := litellm.New(litellm.Config{
			BaseURL:   cfg.LiteLLM.BaseURL,
			MasterKey: cfg.LiteLLM.APIKey,
			Timeout:   cfg.LiteLLM.Timeout,
		}, &http.Client{Timeout: cfg.LiteLLM.Timeout})
		if err != nil {
			return nil, fmt.Errorf("app: litellm provider: %w", err)
		}
		providers["litellm"] = c
	}

	if cfg.Ollama.BaseURL != "" {
		c, err := ollama.New(ollama.Config{
			BaseURL: cfg.Ollama.BaseURL,
			Timeout: cfg.Ollama.Timeout,
		}, &http.Client{Timeout: cfg.Ollama.Timeout})
		if err != nil {
			return nil, fmt.Errorf("app: ollama provider: %w", err)
		}
		providers["ollama"] = c
	}

	if len(providers) == 0 {
		return nil, errors.New("app: llm.enabled=true but no provider has base_url set")
	}

	bindings := map[llm.Role]llm.ProviderBinding{}
	for role, b := range cfg.Bindings {
		p, ok := providers[b.Provider]
		if !ok {
			return nil, fmt.Errorf("app: llm binding %q references unconfigured provider %q",
				role, b.Provider)
		}
		bindings[llm.Role(role)] = llm.ProviderBinding{Provider: p, Model: b.Model}
	}

	registry, err := llm.NewStaticRegistry(bindings)
	if err != nil {
		return nil, fmt.Errorf("app: llm registry: %w", err)
	}

	return &LLMBundle{
		Registry:  registry,
		Providers: providers,
	}, nil
}
