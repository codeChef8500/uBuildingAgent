package llmprovider

import "net/http"

// CacheRetention — prompt cache 保留时长偏好
// provider 按自己支持的机制映射此偏好
type CacheRetention string

const (
	// CacheRetentionNone — 不启用 cache（即使 provider 默认启用也禁用）
	CacheRetentionNone CacheRetention = "none"
	// CacheRetentionShort — 短期 cache（OpenAI ephemeral，Anthropic 5min TTL），默认值
	CacheRetentionShort CacheRetention = "short"
	// CacheRetentionLong — 长期 cache（OpenAI 24h，Anthropic 1h TTL）
	CacheRetentionLong CacheRetention = "long"
)

// Transport — 传输方式偏好
// 不支持该选项的 provider 忽略此字段
type Transport string

const (
	TransportSSE             Transport = "sse"
	TransportWebSocket       Transport = "websocket"
	TransportWebSocketCached Transport = "websocket-cached"
	// TransportAuto — provider 自行选择（默认）
	TransportAuto Transport = "auto"
)

// StreamOptions — 纯请求参数（"怎么问"，与会话内容无关）
// SystemPrompt / Tools / Messages 在 Context 中，不在此处
type StreamOptions struct {
	APIKey string

	// MaxTokens — 最大输出 token 数（0 = 使用模型默认）
	MaxTokens int

	// Temperature — 采样温度（nil = 使用模型默认）
	Temperature *float64

	// OnPayload — 发送前拦截/修改请求体的钩子（nil = 不拦截）
	// 返回 nil 保留原始 payload；返回新 []byte 替换
	OnPayload func(payload []byte, model Model) []byte

	// OnResponse — 收到 HTTP 响应头后的钩子（nil = 不处理）
	OnResponse func(statusCode int, headers http.Header, model Model)

	// SessionId — 会话 ID（用于 prompt cache 亲和性路由）
	SessionId string

	// Metadata — 请求级元数据（provider 提取自己理解的字段，忽略其余）
	Metadata map[string]any

	// TimeoutMs — HTTP 请求超时（毫秒，0 = 使用 provider 默认）
	TimeoutMs int

	// MaxRetries — 最大重试次数（0 = 使用 provider 默认，通常为 2）
	MaxRetries int

	// MaxRetryDelayMs — 重试等待上限（毫秒）
	// 若 provider 要求的 retry-after 超过此值，立即失败而非等待
	// 0 = 无上限（默认 60000，即 60 秒）
	MaxRetryDelayMs int

	// CacheRetention — prompt cache 保留时长偏好（默认 "short"）
	CacheRetention CacheRetention

	// Transport — 传输方式偏好（默认 "auto"）
	Transport Transport

	// Headers — 额外 HTTP 请求头（与 provider 默认头合并，可覆盖）
	Headers map[string]string

	// ExtraBody — 额外请求体字段（provider 特定扩展参数）
	ExtraBody map[string]any
}

// ThinkingLevel — 推理强度级别
type ThinkingLevel string

const (
	// ThinkingLevelOff — 不启用推理（默认）
	ThinkingLevelOff ThinkingLevel = "off"
	// ThinkingLevelMinimal — 最小推理（最小 budget）
	ThinkingLevelMinimal ThinkingLevel = "minimal"
	ThinkingLevelLow     ThinkingLevel = "low"
	ThinkingLevelMedium  ThinkingLevel = "medium"
	ThinkingLevelHigh    ThinkingLevel = "high"
	// ThinkingLevelXHigh — 最高推理（仅部分模型支持，不支持时自动降级为 high）
	ThinkingLevelXHigh ThinkingLevel = "xhigh"
)

// ModelThinkingLevel — 模型侧 thinking level 类型（同 ThinkingLevel，含 "off"）
type ModelThinkingLevel = ThinkingLevel

// ThinkingBudgets — 各 thinking level 对应的 token 预算
type ThinkingBudgets struct {
	Minimal int // 默认 1024
	Low     int // 默认 2048
	Medium  int // 默认 8192
	High    int // 默认 16384
}

// SimpleStreamOptions — 高层请求选项（StreamOptions + Thinking 控制）
// 用于 ApiProvider.StreamSimple()；thinking level 在 provider 内部翻译为 budget
type SimpleStreamOptions struct {
	StreamOptions
	// Reasoning — 推理强度级别（默认 "off"）
	Reasoning ThinkingLevel
	// ThinkingBudgets — 自定义各级别 token 预算（nil = 使用内置默认值）
	ThinkingBudgets *ThinkingBudgets
}
