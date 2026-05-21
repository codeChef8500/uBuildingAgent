// Package openaicompat implements an ApiProvider for OpenAI-compatible APIs.
// A single provider implementation handles OpenAI, DeepSeek, Groq, MiniMax,
// Volcengine Ark, Qwen-compat, Together, Fireworks, OpenRouter, and any
// other provider that speaks the OpenAI chat completions protocol.
package openaicompat

import (
	"strings"

	"github.com/ubuildingagent/backend/llmprovider"
)

// KnownProfiles returns predefined OpenAICompletionsCompat for well-known
// provider base URLs. Returns nil when the URL is not recognised.
func KnownProfiles(baseURL string) *llmprovider.OpenAICompletionsCompat {
	u := strings.ToLower(strings.TrimRight(baseURL, "/"))
	switch {
	case strings.Contains(u, "api.openai.com"):
		return openAIProfile()
	case strings.Contains(u, "api.deepseek.com"):
		return deepSeekProfile()
	case strings.Contains(u, "api.groq.com"):
		return groqProfile()
	case strings.Contains(u, "dashscope.aliyuncs.com/compatible-mode"):
		return qwenCompatProfile()
	case strings.Contains(u, "api.together.xyz") || strings.Contains(u, "api.together.ai"):
		return togetherProfile()
	case strings.Contains(u, "api.fireworks.ai"):
		return fireworksProfile()
	case strings.Contains(u, "openrouter.ai"):
		return openRouterProfile()
	case strings.Contains(u, "ark.cn-beijing.volces.com"),
		strings.Contains(u, "ark.volces.com"):
		return volcengineArkProfile()
	case strings.Contains(u, "api.minimax.chat"):
		return minimaxProfile()
	default:
		return nil
	}
}

// MergeCompat merges override on top of base (override wins for non-nil/non-zero fields).
// Returns base if override is nil.
func MergeCompat(base, override *llmprovider.OpenAICompletionsCompat) *llmprovider.OpenAICompletionsCompat {
	if override == nil {
		return base
	}
	if base == nil {
		return override
	}
	merged := *base // shallow copy
	if override.SupportsStore != nil {
		merged.SupportsStore = override.SupportsStore
	}
	if override.SupportsDeveloperRole != nil {
		merged.SupportsDeveloperRole = override.SupportsDeveloperRole
	}
	if override.SupportsReasoningEffort != nil {
		merged.SupportsReasoningEffort = override.SupportsReasoningEffort
	}
	if override.SupportsUsageInStreaming != nil {
		merged.SupportsUsageInStreaming = override.SupportsUsageInStreaming
	}
	if override.SupportsStrictMode != nil {
		merged.SupportsStrictMode = override.SupportsStrictMode
	}
	if override.RequiresToolResultName != nil {
		merged.RequiresToolResultName = override.RequiresToolResultName
	}
	if override.RequiresAssistantAfterToolResult != nil {
		merged.RequiresAssistantAfterToolResult = override.RequiresAssistantAfterToolResult
	}
	if override.RequiresThinkingAsText != nil {
		merged.RequiresThinkingAsText = override.RequiresThinkingAsText
	}
	if override.RequiresReasoningContentOnReplayed != nil {
		merged.RequiresReasoningContentOnReplayed = override.RequiresReasoningContentOnReplayed
	}
	if override.ThinkingFormat != "" {
		merged.ThinkingFormat = override.ThinkingFormat
	}
	if override.MaxTokensField != "" {
		merged.MaxTokensField = override.MaxTokensField
	}
	if override.CacheControlFormat != "" {
		merged.CacheControlFormat = override.CacheControlFormat
	}
	if override.QwenVideoMode {
		merged.QwenVideoMode = true
	}
	return &merged
}

// ResolveCompat returns the effective compat for a model:
// model.Compat overrides URL-detected profile overrides built-in defaults.
func ResolveCompat(model llmprovider.Model) llmprovider.OpenAICompletionsCompat {
	base := KnownProfiles(model.BaseURL)
	modelCompat := model.OpenAICompat()
	merged := MergeCompat(base, modelCompat)
	if merged == nil {
		return llmprovider.OpenAICompletionsCompat{}
	}
	return *merged
}

// ── Predefined profiles ────────────────────────────────────────────────────

func openAIProfile() *llmprovider.OpenAICompletionsCompat {
	return &llmprovider.OpenAICompletionsCompat{
		SupportsStore:            llmprovider.BoolPtr(true),
		SupportsDeveloperRole:    llmprovider.BoolPtr(true),
		SupportsReasoningEffort:  llmprovider.BoolPtr(true),
		SupportsUsageInStreaming: llmprovider.BoolPtr(true),
		SupportsStrictMode:       llmprovider.BoolPtr(true),
		ThinkingFormat:           llmprovider.ThinkingFormatOpenAI,
	}
}

func deepSeekProfile() *llmprovider.OpenAICompletionsCompat {
	return &llmprovider.OpenAICompletionsCompat{
		SupportsStore:            llmprovider.BoolPtr(false),
		SupportsUsageInStreaming: llmprovider.BoolPtr(true),
		SupportsStrictMode:       llmprovider.BoolPtr(false),
		ThinkingFormat:           llmprovider.ThinkingFormatDeepSeek,
		RequiresThinkingAsText:   llmprovider.BoolPtr(true),
	}
}

func groqProfile() *llmprovider.OpenAICompletionsCompat {
	return &llmprovider.OpenAICompletionsCompat{
		SupportsStore:            llmprovider.BoolPtr(false),
		SupportsUsageInStreaming: llmprovider.BoolPtr(true),
		SupportsStrictMode:       llmprovider.BoolPtr(false),
		ThinkingFormat:           llmprovider.ThinkingFormatOpenAI,
	}
}

func qwenCompatProfile() *llmprovider.OpenAICompletionsCompat {
	return &llmprovider.OpenAICompletionsCompat{
		SupportsStore:            llmprovider.BoolPtr(false),
		SupportsUsageInStreaming: llmprovider.BoolPtr(true),
		SupportsStrictMode:       llmprovider.BoolPtr(false),
		ThinkingFormat:           llmprovider.ThinkingFormatQwen,
		QwenVideoMode:            true,
	}
}

func togetherProfile() *llmprovider.OpenAICompletionsCompat {
	return &llmprovider.OpenAICompletionsCompat{
		SupportsStore:            llmprovider.BoolPtr(false),
		SupportsUsageInStreaming: llmprovider.BoolPtr(true),
		ThinkingFormat:           llmprovider.ThinkingFormatTogether,
	}
}

func fireworksProfile() *llmprovider.OpenAICompletionsCompat {
	return &llmprovider.OpenAICompletionsCompat{
		SupportsStore:            llmprovider.BoolPtr(false),
		SupportsUsageInStreaming: llmprovider.BoolPtr(false),
		CacheControlFormat:       "anthropic",
		ThinkingFormat:           llmprovider.ThinkingFormatOpenAI,
	}
}

func openRouterProfile() *llmprovider.OpenAICompletionsCompat {
	return &llmprovider.OpenAICompletionsCompat{
		SupportsStore:                    llmprovider.BoolPtr(false),
		SupportsUsageInStreaming:          llmprovider.BoolPtr(true),
		RequiresAssistantAfterToolResult: llmprovider.BoolPtr(true),
		ThinkingFormat:                   llmprovider.ThinkingFormatOpenRouter,
	}
}

// volcengineArkProfile — Volcengine Ark (ByteDance) OpenAI-compat endpoint
// Used for MiniMax, Doubao, and other models hosted on Ark
func volcengineArkProfile() *llmprovider.OpenAICompletionsCompat {
	return &llmprovider.OpenAICompletionsCompat{
		SupportsStore:            llmprovider.BoolPtr(false),
		SupportsUsageInStreaming: llmprovider.BoolPtr(true),
		SupportsStrictMode:       llmprovider.BoolPtr(false),
		ThinkingFormat:           llmprovider.ThinkingFormatOpenAI,
	}
}

func minimaxProfile() *llmprovider.OpenAICompletionsCompat {
	return &llmprovider.OpenAICompletionsCompat{
		SupportsStore:            llmprovider.BoolPtr(false),
		SupportsUsageInStreaming: llmprovider.BoolPtr(true),
		SupportsStrictMode:       llmprovider.BoolPtr(false),
		ThinkingFormat:           llmprovider.ThinkingFormatOpenAI,
	}
}
