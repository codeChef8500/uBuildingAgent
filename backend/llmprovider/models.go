package llmprovider

// ApiType — API 协议类型（Registry 路由主键）
// 与 provider 公司名解耦：同一公司可有多个协议（如 openai-completions vs openai-responses）
type ApiType string

const (
	ApiOpenAICompletions    ApiType = "openai-completions"
	ApiOpenAIResponses      ApiType = "openai-responses"
	ApiAnthropicMessages    ApiType = "anthropic-messages"
	ApiGoogleGenerativeAI   ApiType = "google-generative-ai"
	ApiGoogleVertex         ApiType = "google-vertex"
	ApiBedrockConverse      ApiType = "bedrock-converse-stream"
	ApiDashScopeMessages    ApiType = "dashscope-messages" // Qwen 原生
	ApiMistralConversations ApiType = "mistral-conversations"
	ApiAzureOpenAIResponses ApiType = "azure-openai-responses"
)

// Modality — 模型支持的输入/输出模态
type Modality string

const (
	ModalityText     Modality = "text"
	ModalityVision   Modality = "vision"   // 单图/多图
	ModalityVideo    Modality = "video"    // 多帧视频 / 视频 URL
	ModalityThinking Modality = "thinking" // 推理/思维链
)

// ThinkingLevelMap — per-model thinking level 映射
// key: ThinkingLevel；value: provider 特定字符串值（nil = 该级别不支持）
// 示例（claude-3-7-sonnet）: "low"->"1024", "medium"->"8192", "xhigh"->nil（不支持）
type ThinkingLevelMap map[ModelThinkingLevel]*string

// Model — 运行时模型对象（路由信息 + 能力元数据）
// 传递给 ApiProvider.Stream() / StreamSimple()
type Model struct {
	// ── 路由字段 ──
	ID       string  `json:"id"`       // 模型 ID，如 "gpt-4o"
	Api      ApiType `json:"api"`      // 协议类型，用于 Registry 路由
	Provider string  `json:"provider"` // 公司名（仅元数据，不用于路由）

	// ── 请求时必需 ──
	BaseURL string            `json:"base_url"`          // provider endpoint（per-model 可覆盖）
	Headers map[string]string `json:"headers,omitempty"` // per-model 自定义请求头

	// ── 兼容性覆盖（ApiType 特定，按需类型断言）──
	// ApiOpenAICompletions → *OpenAICompletionsCompat
	// ApiAnthropicMessages → *AnthropicMessagesCompat
	Compat any `json:"compat,omitempty"`

	// ── 能力元数据 ──
	Name          string     `json:"name"`
	Modalities    []Modality `json:"modalities"`
	ContextWindow int        `json:"context_window"`
	MaxOutput     int        `json:"max_output"`

	// ── 计费元数据 ──
	InputCostPer1K  float64 `json:"input_cost_per_1k"`
	OutputCostPer1K float64 `json:"output_cost_per_1k"`

	// ── 能力标志 ──
	SupportsTools bool `json:"supports_tools"`
	Reasoning     bool `json:"reasoning"` // 是否支持 thinking/reasoning

	// ThinkingLevelMap — per-model thinking level → provider 特定值的映射
	// nil = 使用 AdjustMaxTokensForThinking 默认 budget
	ThinkingLevelMap ThinkingLevelMap `json:"thinking_level_map,omitempty"`
}

// OpenAICompat 从 Model 中安全提取 *OpenAICompletionsCompat（nil = 无覆盖配置）
func (m Model) OpenAICompat() *OpenAICompletionsCompat {
	if m.Compat == nil {
		return nil
	}
	c, _ := m.Compat.(*OpenAICompletionsCompat)
	return c
}

// AnthropicCompat 从 Model 中安全提取 *AnthropicMessagesCompat（nil = 无覆盖配置）
func (m Model) AnthropicCompat() *AnthropicMessagesCompat {
	if m.Compat == nil {
		return nil
	}
	c, _ := m.Compat.(*AnthropicMessagesCompat)
	return c
}

// BuiltinModels — 内置模型目录（含主流 LLM/VLM 模型）
func BuiltinModels() []Model {
	trueVal := BoolPtr(true)
	falseVal := BoolPtr(false)

	return []Model{
		// ── OpenAI ──
		{
			ID: "gpt-4o", Api: ApiOpenAICompletions, Provider: "openai",
			BaseURL: "https://api.openai.com/v1",
			Name:    "GPT-4o",
			Modalities:    []Modality{ModalityText, ModalityVision},
			ContextWindow: 128000, MaxOutput: 16384,
			InputCostPer1K: 0.0025, OutputCostPer1K: 0.01,
			SupportsTools: true,
			Compat: &OpenAICompletionsCompat{
				SupportsStore: trueVal, SupportsDeveloperRole: trueVal,
				SupportsUsageInStreaming: trueVal, SupportsStrictMode: trueVal,
			},
		},
		{
			ID: "gpt-4o-mini", Api: ApiOpenAICompletions, Provider: "openai",
			BaseURL: "https://api.openai.com/v1",
			Name:    "GPT-4o Mini",
			Modalities:    []Modality{ModalityText, ModalityVision},
			ContextWindow: 128000, MaxOutput: 16384,
			InputCostPer1K: 0.00015, OutputCostPer1K: 0.0006,
			SupportsTools: true,
			Compat: &OpenAICompletionsCompat{
				SupportsStore: trueVal, SupportsDeveloperRole: trueVal,
				SupportsUsageInStreaming: trueVal, SupportsStrictMode: trueVal,
			},
		},
		{
			ID: "o3-mini", Api: ApiOpenAICompletions, Provider: "openai",
			BaseURL:  "https://api.openai.com/v1",
			Name:     "o3-mini",
			Modalities:    []Modality{ModalityText, ModalityThinking},
			ContextWindow: 200000, MaxOutput: 100000,
			InputCostPer1K: 0.0011, OutputCostPer1K: 0.0044,
			SupportsTools: true, Reasoning: true,
			Compat: &OpenAICompletionsCompat{
				SupportsStore: trueVal, SupportsDeveloperRole: trueVal,
				SupportsUsageInStreaming: trueVal, SupportsStrictMode: trueVal,
				SupportsReasoningEffort: trueVal, MaxTokensField: "max_completion_tokens",
			},
			ThinkingLevelMap: ThinkingLevelMap{
				ThinkingLevelLow:    strPtr("low"),
				ThinkingLevelMedium: strPtr("medium"),
				ThinkingLevelHigh:   strPtr("high"),
			},
		},
		// ── Anthropic ──
		{
			ID: "claude-3-5-sonnet-20241022", Api: ApiAnthropicMessages, Provider: "anthropic",
			BaseURL: "https://api.anthropic.com",
			Name:    "Claude 3.5 Sonnet",
			Modalities:    []Modality{ModalityText, ModalityVision},
			ContextWindow: 200000, MaxOutput: 8192,
			InputCostPer1K: 0.003, OutputCostPer1K: 0.015,
			SupportsTools: true,
			Compat: &AnthropicMessagesCompat{
				SupportsEagerToolInputStreaming: trueVal,
				SupportsLongCacheRetention:     trueVal,
				SupportsCacheControlOnTools:    trueVal,
			},
		},
		{
			ID: "claude-3-7-sonnet-20250219", Api: ApiAnthropicMessages, Provider: "anthropic",
			BaseURL: "https://api.anthropic.com",
			Name:    "Claude 3.7 Sonnet",
			Modalities:    []Modality{ModalityText, ModalityVision, ModalityThinking},
			ContextWindow: 200000, MaxOutput: 64000,
			InputCostPer1K: 0.003, OutputCostPer1K: 0.015,
			SupportsTools: true, Reasoning: true,
			Compat: &AnthropicMessagesCompat{
				SupportsEagerToolInputStreaming: trueVal,
				SupportsLongCacheRetention:     trueVal,
				SupportsCacheControlOnTools:    trueVal,
			},
			ThinkingLevelMap: ThinkingLevelMap{
				ThinkingLevelMinimal: strPtr("1024"),
				ThinkingLevelLow:     strPtr("2048"),
				ThinkingLevelMedium:  strPtr("8192"),
				ThinkingLevelHigh:    strPtr("16384"),
			},
		},
		// ── Google ──
		{
			ID: "gemini-2.0-flash", Api: ApiGoogleGenerativeAI, Provider: "google",
			BaseURL: "https://generativelanguage.googleapis.com",
			Name:    "Gemini 2.0 Flash",
			Modalities:    []Modality{ModalityText, ModalityVision, ModalityVideo},
			ContextWindow: 1048576, MaxOutput: 8192,
			InputCostPer1K: 0.0001, OutputCostPer1K: 0.0004,
			SupportsTools: true,
		},
		{
			ID: "gemini-2.5-pro", Api: ApiGoogleGenerativeAI, Provider: "google",
			BaseURL: "https://generativelanguage.googleapis.com",
			Name:    "Gemini 2.5 Pro",
			Modalities:    []Modality{ModalityText, ModalityVision, ModalityVideo, ModalityThinking},
			ContextWindow: 1048576, MaxOutput: 65536,
			InputCostPer1K: 0.00125, OutputCostPer1K: 0.01,
			SupportsTools: true, Reasoning: true,
		},
		// ── Qwen DashScope 原生 ──
		{
			ID: "qwen2.5-vl-72b-instruct", Api: ApiDashScopeMessages, Provider: "qwen",
			BaseURL: "https://dashscope.aliyuncs.com/api/v1",
			Name:    "Qwen2.5-VL-72B",
			Modalities:    []Modality{ModalityText, ModalityVision, ModalityVideo},
			ContextWindow: 128000, MaxOutput: 8192,
			InputCostPer1K: 0.004, OutputCostPer1K: 0.012,
			SupportsTools: true,
		},
		{
			ID: "qwen2.5-vl-7b-instruct", Api: ApiDashScopeMessages, Provider: "qwen",
			BaseURL: "https://dashscope.aliyuncs.com/api/v1",
			Name:    "Qwen2.5-VL-7B",
			Modalities:    []Modality{ModalityText, ModalityVision, ModalityVideo},
			ContextWindow: 128000, MaxOutput: 8192,
			InputCostPer1K: 0.001, OutputCostPer1K: 0.002,
			SupportsTools: true,
		},
		{
			ID: "qwen2-vl-72b-instruct", Api: ApiDashScopeMessages, Provider: "qwen",
			BaseURL: "https://dashscope.aliyuncs.com/api/v1",
			Name:    "Qwen2-VL-72B",
			Modalities:    []Modality{ModalityText, ModalityVision, ModalityVideo},
			ContextWindow: 128000, MaxOutput: 8192,
			InputCostPer1K: 0.004, OutputCostPer1K: 0.012,
			SupportsTools: true,
		},
		// ── Qwen OpenAI-compat ──
		{
			ID: "qwen2.5-vl-72b-instruct", Api: ApiOpenAICompletions, Provider: "qwen-compat",
			BaseURL: "https://dashscope.aliyuncs.com/compatible-mode/v1",
			Name:    "Qwen2.5-VL-72B (compat)",
			Modalities:    []Modality{ModalityText, ModalityVision, ModalityVideo},
			ContextWindow: 128000, MaxOutput: 8192,
			InputCostPer1K: 0.004, OutputCostPer1K: 0.012,
			SupportsTools: true,
			Compat: &OpenAICompletionsCompat{
				QwenVideoMode:           true,
				SupportsUsageInStreaming: trueVal,
				SupportsStrictMode:      falseVal,
			},
		},
		// ── DeepSeek ──
		{
			ID: "deepseek-chat", Api: ApiOpenAICompletions, Provider: "deepseek",
			BaseURL: "https://api.deepseek.com/v1",
			Name:    "DeepSeek Chat",
			Modalities:    []Modality{ModalityText},
			ContextWindow: 64000, MaxOutput: 8192,
			InputCostPer1K: 0.00014, OutputCostPer1K: 0.00028,
			SupportsTools: true,
			Compat: &OpenAICompletionsCompat{
				SupportsStore:           falseVal,
				SupportsUsageInStreaming: trueVal,
			},
		},
		{
			ID: "deepseek-reasoner", Api: ApiOpenAICompletions, Provider: "deepseek",
			BaseURL: "https://api.deepseek.com/v1",
			Name:    "DeepSeek R1",
			Modalities:    []Modality{ModalityText, ModalityThinking},
			ContextWindow: 64000, MaxOutput: 8192,
			InputCostPer1K: 0.00055, OutputCostPer1K: 0.00219,
			SupportsTools: false, Reasoning: true,
			Compat: &OpenAICompletionsCompat{
				SupportsStore:          falseVal,
				ThinkingFormat:         ThinkingFormatDeepSeek,
				RequiresThinkingAsText: trueVal,
			},
		},
	}
}

// strPtr — 返回字符串值的指针（用于 ThinkingLevelMap）
func strPtr(s string) *string {
	return &s
}
