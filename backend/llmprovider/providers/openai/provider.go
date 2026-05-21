// Package openai provides a factory for the OpenAI provider.
// It is a thin wrapper around openaicompat.Provider pre-configured for OpenAI's
// base URL and capabilities (o-series max_completion_tokens, developer role, etc.).
package openai

import (
	"context"
	"net/http"

	"github.com/ubuildingagent/backend/llmprovider"
	"github.com/ubuildingagent/backend/llmprovider/providers/openaicompat"
)

// Provider wraps openaicompat.Provider, exposing ApiType = ApiOpenAICompletions.
// Model-level compat (e.g. max_completion_tokens for o-series) is applied via
// Model.Compat fields set in models.go BuiltinModels().
type Provider struct {
	inner *openaicompat.Provider
}

// New creates an OpenAI provider with default HTTP client.
func New() *Provider {
	return &Provider{inner: openaicompat.New()}
}

// NewWithClient creates an OpenAI provider with a custom HTTP client.
func NewWithClient(client *http.Client) *Provider {
	return &Provider{inner: openaicompat.NewWithClient(client)}
}

func (p *Provider) ApiType() llmprovider.ApiType {
	return llmprovider.ApiOpenAICompletions
}

func (p *Provider) Stream(
	ctx context.Context,
	model llmprovider.Model,
	conv llmprovider.Context,
	opts llmprovider.StreamOptions,
) <-chan llmprovider.StreamEvent {
	return p.inner.Stream(ctx, model, conv, opts)
}

func (p *Provider) StreamSimple(
	ctx context.Context,
	model llmprovider.Model,
	conv llmprovider.Context,
	opts llmprovider.SimpleStreamOptions,
) <-chan llmprovider.StreamEvent {
	return p.inner.StreamSimple(ctx, model, conv, opts)
}
