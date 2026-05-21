package llmprovider

import (
	"encoding/json"
)

// ContentType — 多模态消息块类型
type ContentType string

const (
	ContentTypeText       ContentType = "text"
	ContentTypeImageURL   ContentType = "image_url"    // URL 图像
	ContentTypeImageData  ContentType = "image_data"   // base64 内嵌
	ContentTypeVideoFrame ContentType = "video_frame"  // 视频帧（多帧逐帧 bytes）
	ContentTypeVideoURL   ContentType = "video_url"    // 视频 URL（Qwen/Gemini 原生支持）
	ContentTypeToolResult ContentType = "tool_result"
)

// ContentPart — 多模态消息块，所有字段按 Type 按需使用
type ContentPart struct {
	Type     ContentType `json:"type"`
	Text     string      `json:"text,omitempty"`
	ImageURL string      `json:"image_url,omitempty"`
	Data     []byte      `json:"data,omitempty"`      // raw bytes（image_data / video_frame）
	MimeType string      `json:"mime_type,omitempty"` // image/jpeg, image/png

	// video_frame 专属
	FrameIndex  int     `json:"frame_index,omitempty"`
	TimestampMs float64 `json:"timestamp_ms,omitempty"`

	// video_url 专属（Qwen DashScope / Google Gemini 原生视频 URL）
	VideoURL     string  `json:"video_url,omitempty"`     // "https://xxx.mp4"
	VideoFPS     float64 `json:"video_fps,omitempty"`     // 采样帧率（可选）
	VideoNFrames int     `json:"video_nframes,omitempty"` // 固定帧数（可选）

	// tool_result 专属
	ToolCallID string `json:"tool_call_id,omitempty"`
	ToolResult string `json:"tool_result,omitempty"`
}

// Role — 消息角色
type Role string

const (
	RoleSystem    Role = "system"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
)

// ToolCall — 工具调用块（来自 assistant 消息）
type ToolCall struct {
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
	// Google Thinking 上下文复用：不透明签名，回传给 Google API 以复用 thought context
	ThoughtSignature string `json:"thought_signature,omitempty"`
}

// ToolCallDelta — 流式工具调用增量
type ToolCallDelta struct {
	Index     int    `json:"index"`
	ID        string `json:"id,omitempty"`
	Name      string `json:"name,omitempty"`
	ArgsDelta string `json:"args_delta,omitempty"`
}

// Message — 统一消息格式（用于 llmprovider 层传递）
type Message struct {
	Role      Role          `json:"role"`
	Content   []ContentPart `json:"content"`
	ToolCalls []ToolCall    `json:"tool_calls,omitempty"`
	// thinking 为 assistant 消息的推理过程（Anthropic extended thinking 等）
	Thinking string `json:"thinking,omitempty"`
}

// Tool — 工具定义（传给 LLM 的工具描述）
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	Parameters  json.RawMessage `json:"parameters"` // JSON Schema
}

// Context — 会话状态（每次 LLM 调用携带，表示"要问什么"）
// 与 StreamOptions（"怎么问"）严格分离
type Context struct {
	SystemPrompt string    `json:"system_prompt,omitempty"`
	Messages     []Message `json:"messages"`
	Tools        []Tool    `json:"tools,omitempty"`
}

// StreamEventType — 流式事件类型
type StreamEventType string

const (
	StreamEventTextDelta     StreamEventType = "text_delta"
	StreamEventThinkingDelta StreamEventType = "thinking_delta"
	StreamEventToolCallStart StreamEventType = "tool_call_start"
	StreamEventToolCallDelta StreamEventType = "tool_call_delta"
	StreamEventToolCallEnd   StreamEventType = "tool_call_end"
	StreamEventMessageEnd    StreamEventType = "message_end"
	StreamEventError         StreamEventType = "error"
)

// StreamEvent — 单个流式事件
type StreamEvent struct {
	Type       StreamEventType `json:"type"`
	Delta      string          `json:"delta,omitempty"`
	ToolCall   *ToolCallDelta  `json:"tool_call,omitempty"`
	Usage      *Usage          `json:"usage,omitempty"`
	StopReason StopReason      `json:"stop_reason,omitempty"`
	Err        error           `json:"-"`
}

// StopReason — 流终止原因
type StopReason string

const (
	StopReasonStop    StopReason = "stop"
	StopReasonLength  StopReason = "length"
	StopReasonToolUse StopReason = "tool_use"
	StopReasonError   StopReason = "error"
	StopReasonAborted StopReason = "aborted"
)

// Usage — Token 用量统计（含 cache）
type Usage struct {
	InputTokens       int     `json:"input_tokens"`
	OutputTokens      int     `json:"output_tokens"`
	CacheReadTokens   int     `json:"cache_read_tokens"`
	CacheCreateTokens int     `json:"cache_create_tokens"`
	CostUSD           float64 `json:"cost_usd"`
}

// Diagnostic — provider 运行时诊断（脱敏，不含敏感数据）
// 包含重试/降级/限速等信息，供调试使用
type Diagnostic struct {
	Type    string `json:"type"`    // "retry", "fallback", "rate_limit", etc.
	Message string `json:"message"`
}

// StreamResult — 流结束后的聚合结果
type StreamResult struct {
	Usage      Usage
	StopReason StopReason
	// ResponseModel — 实际使用的模型（如 OpenRouter auto 路由后的具体模型）
	ResponseModel string
	// ResponseID — provider 特定响应 ID（用于问题追踪）
	ResponseID  string
	Diagnostics []Diagnostic
	Err         error
}
