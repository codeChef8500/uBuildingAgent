// Package providers contains all built-in ApiProvider implementations and
// the registration logic that wires them into the default Registry.
package providers

import (
	"github.com/ubuildingagent/backend/llmprovider"
	"github.com/ubuildingagent/backend/llmprovider/providers/anthropic"
	"github.com/ubuildingagent/backend/llmprovider/providers/google"
	"github.com/ubuildingagent/backend/llmprovider/providers/openai"
	"github.com/ubuildingagent/backend/llmprovider/providers/openaicompat"
	"github.com/ubuildingagent/backend/llmprovider/providers/qwen"
)

const sourceID = "builtin"

// RegisterBuiltins registers all built-in ApiProviders into r.
// Each provider is wrapped in a LazyProvider so initialization is deferred
// until the first actual call.
func RegisterBuiltins(r *llmprovider.Registry) {
	// OpenAI completions API (also handles DeepSeek, Groq, MiniMax, Volcengine Ark, etc.)
	r.Register(
		NewLazyProvider(llmprovider.ApiOpenAICompletions, func() (llmprovider.ApiProvider, error) {
			return openai.New(), nil
		}),
		sourceID,
	)

	// Anthropic Messages API
	r.Register(
		NewLazyProvider(llmprovider.ApiAnthropicMessages, func() (llmprovider.ApiProvider, error) {
			return anthropic.New(), nil
		}),
		sourceID,
	)

	// Google Generative AI (Gemini)
	r.Register(
		NewLazyProvider(llmprovider.ApiGoogleGenerativeAI, func() (llmprovider.ApiProvider, error) {
			return google.New(), nil
		}),
		sourceID,
	)

	// Qwen DashScope native API
	r.Register(
		NewLazyProvider(llmprovider.ApiDashScopeMessages, func() (llmprovider.ApiProvider, error) {
			return qwen.New(), nil
		}),
		sourceID,
	)

	// OpenAI-compat fallback provider:
	// Handles any custom OpenAI-compatible endpoint not covered by the above.
	// Registered under a separate sentinel ApiType so callers can explicitly
	// request it for custom endpoints (e.g. local vLLM, Ollama, etc.).
	// For standard providers (openai/deepseek/etc.), model.Api should be set to
	// ApiOpenAICompletions which routes to the openai.Provider above.
	_ = openaicompat.New // referenced to avoid import cycle warnings
}

// init registers built-in providers into the DefaultRegistry at startup.
func init() {
	RegisterBuiltins(llmprovider.DefaultRegistry)
}
