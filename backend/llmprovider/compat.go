package llmprovider

// ThinkingFormat — OpenAI-compat provider 的 reasoning 参数编码格式
// 不同 provider 传递 thinking 参数的字段名和结构各不相同
type ThinkingFormat string

const (
	// ThinkingFormatOpenAI — reasoning_effort: "low"|"medium"|"high"
	ThinkingFormatOpenAI ThinkingFormat = "openai"
	// ThinkingFormatDeepSeek — thinking: {type:"enabled_budget",...} + reasoning_effort
	ThinkingFormatDeepSeek ThinkingFormat = "deepseek"
	// ThinkingFormatQwen — 顶层 enable_thinking: true
	ThinkingFormatQwen ThinkingFormat = "qwen"
	// ThinkingFormatQwenChatTpl — chat_template_kwargs: {enable_thinking: true}
	ThinkingFormatQwenChatTpl ThinkingFormat = "qwen-chat-template"
	// ThinkingFormatTogether — reasoning: {enabled: true} + reasoning_effort
	ThinkingFormatTogether ThinkingFormat = "together"
	// ThinkingFormatOpenRouter — reasoning: {effort: "low"|"medium"|"high"}
	ThinkingFormatOpenRouter ThinkingFormat = "openrouter"
)

// OpenAICompletionsCompat — per-model OpenAI completions 协议兼容性覆盖
// 放在 Model.Compat 字段中（ApiType == ApiOpenAICompletions 时）
// nil 指针字段表示"自动从 baseUrl 检测"
type OpenAICompletionsCompat struct {
	// ── 功能支持标志（nil = 自动从 baseUrl 检测）──

	// SupportsStore — 是否支持 `store` 字段
	SupportsStore *bool
	// SupportsDeveloperRole — 是否支持 `developer` role（而非 `system`）
	SupportsDeveloperRole *bool
	// SupportsReasoningEffort — 是否支持 `reasoning_effort` 字段
	SupportsReasoningEffort *bool
	// SupportsUsageInStreaming — 是否支持 `stream_options: {include_usage: true}`
	SupportsUsageInStreaming *bool
	// SupportsStrictMode — 是否支持 tool definition 中的 `strict` 字段
	SupportsStrictMode *bool

	// ── 消息格式强制要求 ──

	// RequiresToolResultName — tool result 消息是否必须有 `name` 字段
	RequiresToolResultName *bool
	// RequiresAssistantAfterToolResult — tool result 后是否必须有 assistant 消息
	RequiresAssistantAfterToolResult *bool
	// RequiresThinkingAsText — thinking block 是否必须转为 <thinking>...</thinking> 文本块
	RequiresThinkingAsText *bool
	// RequiresReasoningContentOnReplayed — 重放的 assistant 消息是否必须包含空 reasoning_content
	RequiresReasoningContentOnReplayed *bool

	// ── Thinking/Reasoning 格式 ──
	// 控制如何传递 reasoning 参数（空字符串 = 默认 "openai"）
	ThinkingFormat ThinkingFormat

	// MaxTokensField — 最大 token 字段名
	// "max_completion_tokens"（OpenAI o 系列）或 "max_tokens"（默认，空 = "max_tokens"）
	MaxTokensField string

	// CacheControlFormat — prompt cache 标记格式
	// "" = 不添加 cache control 标记
	// "anthropic" = 在 system prompt、最后一个 tool 定义、最后一条 user/assistant 文本上
	//               添加 Anthropic 风格的 cache_control 标记
	CacheControlFormat string

	// QwenVideoMode — 是否启用 Qwen video content type 扩展
	// true = video_url/video_frame ContentPart 转换为 Qwen {"type":"video",...} 格式
	QwenVideoMode bool
}

// AnthropicMessagesCompat — per-model Anthropic Messages 协议兼容性覆盖
// 放在 Model.Compat 字段中（ApiType == ApiAnthropicMessages 时）
type AnthropicMessagesCompat struct {
	// SupportsEagerToolInputStreaming — 是否接受 per-tool eager_input_streaming 字段
	// false 时使用 legacy fine-grained-tool-streaming beta header
	SupportsEagerToolInputStreaming *bool
	// SupportsLongCacheRetention — 是否支持 cache_control.ttl: "1h"（长期 cache）
	SupportsLongCacheRetention *bool
	// SendSessionAffinityHeaders — 是否发送 x-session-affinity 头（prompt cache 路由亲和性）
	SendSessionAffinityHeaders *bool
	// SupportsCacheControlOnTools — tool 定义是否接受 cache_control 字段
	// false 时从 tool params 中省略 cache_control（部分 Anthropic-compat provider 不支持）
	SupportsCacheControlOnTools *bool
}

// BoolPtr — 返回 bool 值的指针（用于 Compat 字段赋值）
func BoolPtr(b bool) *bool {
	return &b
}

// BoolVal — 安全读取 *bool 值，nil 时返回 defaultVal
func BoolVal(p *bool, defaultVal bool) bool {
	if p == nil {
		return defaultVal
	}
	return *p
}
