# backend llmprovider + agentcore 模块设计（VLM 支持，含 Qwen）

支持单图/多图/多帧视频/视频 URL 输入的 Go 语言 LLM/VLM 提供商抽象层与 Agent 运行时核心设计方案，包含 OpenAI、Anthropic、Google Gemini、Qwen（DashScope 原生 + OpenAI 兼容）四大提供商。

---

## 技术栈推荐：Go

| 维度 | Go 的优势 |
|------|---------|
| **并发** | goroutine + channel，天然支持数千并发流式 LLM 调用 |
| **部署** | 编译为单二进制，无运行时依赖，容器镜像极小 |
| **流式** | `io.Reader` / SSE 处理简洁，适合 LLM streaming |
| **类型安全** | 强类型接口，VLM 多模态内容用 discriminated struct |
| **mass 平台** | 低内存占用，单机数万并发连接无压力 |

---

## 一、目录结构

```
backend/
├── llmprovider/
│   ├── types.go          # ContentPart, Message, Tool, StreamEvent
│   ├── provider.go       # Provider 接口 + Registry
│   ├── registry.go       # 注册表实现
│   ├── stream.go         # SSE parser, channel fan-out
│   ├── models.go         # 模型元数据（能力标记：vision/video/thinking）
│   ├── cost.go           # Token/Cost 追踪
│   └── providers/
│       ├── openai/       # OpenAI (gpt-4o, o1)
│       ├── anthropic/    # Anthropic (claude-3.5)
│       ├── google/       # Google Gemini (gemini-2.0)
│       ├── qwen/         # Qwen DashScope 原生（video_url 直传，X-DashScope-SSE）
│       │   ├── provider.go
│       │   ├── convert.go  # ContentPart → DashScope 格式（三种视频路径）
│       │   ├── sse.go      # DashScope SSE 解析
│       │   └── models.go   # Qwen 模型目录（VLM 能力标记）
│       └── openaicompat/ # OpenAI 兼容（DeepSeek, Groq, Qwen-compat 等）
│           ├── provider.go
│           └── qwen_video.go  # Qwen video type 扩展处理
│
└── agentcore/
    ├── types.go          # AgentMessage, AgentEvent, AgentTool, AgentState
    ├── agent.go          # Agent struct（状态管理）
    ├── loop.go           # Agent Loop 核心循环
    ├── budget.go         # IterationBudget（跨子 Agent 共享预算）
    ├── tool_registry.go  # 工具注册表（TTL check_fn）
    ├── hooks.go          # BeforeToolCall / AfterToolCall
    ├── convert.go        # AgentMessage → LLM Message 过滤
    ├── context/          # 上下文管理（compaction/压缩）
    │   ├── types.go      # CompactionSettings, CompactionResult, ContextEstimate
    │   ├── engine.go     # ContextEngine 接口（可插拔压缩引擎）
    │   ├── compactor.go  # 默认 Compactor 实现（head/tail 保护 + summarization）
    │   ├── estimator.go  # Token 估算（字符启发式 + usage 校准）
    │   └── pipeline.go   # 多层压缩 pipeline（tool pruning → compaction）
    └── session/          # 会话管理（持久化/恢复/fork）
        ├── types.go      # SessionEntry, SessionMetadata, SessionContext
        ├── storage.go    # SessionStorage 接口
        ├── memory.go     # InMemoryStorage 实现
        ├── jsonl.go      # JSONL 文件存储实现
        └── repo.go       # SessionRepo: Create/Open/List/Delete/Fork
```

---

## 二、llmprovider 核心类型

### 2.1 多模态内容（VLM 核心）

```go
// types.go

type ContentType string

const (
    ContentTypeText       ContentType = "text"
    ContentTypeImageURL   ContentType = "image_url"    // URL 图像
    ContentTypeImageData  ContentType = "image_data"   // base64 内嵌
    ContentTypeVideoFrame ContentType = "video_frame"  // 视频帧（多帧逐帧 bytes）
    ContentTypeVideoURL   ContentType = "video_url"    // 视频 URL（Qwen/Gemini 原生支持）
    ContentTypeToolResult ContentType = "tool_result"
)

// ContentPart — 多模态消息块
type ContentPart struct {
    Type     ContentType `json:"type"`
    Text     string      `json:"text,omitempty"`
    ImageURL string      `json:"image_url,omitempty"`
    Data     []byte      `json:"data,omitempty"`       // raw bytes
    MimeType string      `json:"mime_type,omitempty"`  // image/jpeg, image/png
    // video_frame 专属
    FrameIndex  int     `json:"frame_index,omitempty"`
    TimestampMs float64 `json:"timestamp_ms,omitempty"`
    // video_url 专属（Qwen DashScope / Google Gemini 原生视频 URL）
    VideoURL     string  `json:"video_url,omitempty"`      // "https://xxx.mp4"
    VideoFPS     float64 `json:"video_fps,omitempty"`      // 采样帧率（可选）
    VideoNFrames int     `json:"video_nframes,omitempty"`  // 固定帧数（可选）
    // tool_result 专属
    ToolCallID  string  `json:"tool_call_id,omitempty"`
    ToolResult  string  `json:"tool_result,omitempty"`
}

// VideoInput — 多帧视频便捷封装
type VideoInput struct {
    Frames    []VideoFrame
    TotalMs   float64
    SampleFPS float64
}

type VideoFrame struct {
    Data        []byte
    MimeType    string  // image/jpeg
    TimestampMs float64
}

// NewVideoFrames 将 VideoInput 转换为 ContentPart 序列（video_frame 类型）
func NewVideoFrames(v VideoInput) []ContentPart

// NewVideoURL 创建视频 URL 类型的 ContentPart（Qwen/Gemini 最优路径）
func NewVideoURL(url string, fps float64) ContentPart

// NewVideoFrameSeq 将 []VideoFrame 转换为 base64 数组模式的多帧序列
func NewVideoFrameSeq(frames []VideoFrame) []ContentPart

// GroupVideoFrames 将连续的 video_frame ContentPart 合并为可传给 Qwen 的单块
func GroupVideoFrames(parts []ContentPart) []ContentPart
```

### 2.2 统一消息格式

```go
type Role string

const (
    RoleSystem    Role = "system"
    RoleUser      Role = "user"
    RoleAssistant Role = "assistant"
    RoleTool      Role = "tool"
)

type Message struct {
    Role      Role          `json:"role"`
    Content   []ContentPart `json:"content"`
    ToolCalls []ToolCall    `json:"tool_calls,omitempty"`
    Thinking  string        `json:"thinking,omitempty"`
}

type ToolCall struct {
    ID        string          `json:"id"`
    Name      string          `json:"name"`
    Arguments json.RawMessage `json:"arguments"`
}
```

### 2.3 流式事件

```go
type StreamEventType string

const (
    StreamEventTextDelta     StreamEventType = "text_delta"
    StreamEventThinking      StreamEventType = "thinking_delta"
    StreamEventToolCallStart StreamEventType = "tool_call_start"
    StreamEventToolCallDelta StreamEventType = "tool_call_delta"
    StreamEventToolCallEnd   StreamEventType = "tool_call_end"
    StreamEventMessageEnd    StreamEventType = "message_end"
    StreamEventError         StreamEventType = "error"
)

type StreamEvent struct {
    Type       StreamEventType `json:"type"`
    Delta      string          `json:"delta,omitempty"`
    ToolCall   *ToolCallDelta  `json:"tool_call,omitempty"`
    Usage      *Usage          `json:"usage,omitempty"`
    StopReason string          `json:"stop_reason,omitempty"`
    Err        error           `json:"-"`
}

type Usage struct {
    InputTokens       int     `json:"input_tokens"`
    OutputTokens      int     `json:"output_tokens"`
    CacheReadTokens   int     `json:"cache_read_tokens"`
    CacheCreateTokens int     `json:"cache_create_tokens"`
    CostUSD           float64 `json:"cost_usd"`
}
```

### 2.4 Provider 接口 + Registry

```go
// provider.go

type Modality string

const (
    ModalityText     Modality = "text"
    ModalityVision   Modality = "vision"   // 单图/多图
    ModalityVideo    Modality = "video"    // 多帧视频
    ModalityThinking Modality = "thinking"
)

type StreamOptions struct {
    MaxTokens      int
    Temperature    *float64
    Tools          []Tool
    ThinkingBudget int
    SystemPrompt   string
    ExtraHeaders   map[string]string
    ExtraBody      map[string]any
}

// Provider — 唯一核心接口
type Provider interface {
    Name() string
    SupportedModalities() []Modality
    // Stream 返回只读 channel，close 表示结束
    Stream(ctx context.Context, model string, messages []Message, opts StreamOptions) (<-chan StreamEvent, error)
}

// registry.go
type Registry struct {
    mu        sync.RWMutex
    providers map[string]Provider
}

func (r *Registry) Register(p Provider)
func (r *Registry) Get(name string) (Provider, bool)
func (r *Registry) List() []Provider
```

### 2.5 模型元数据

```go
// models.go

type ModelCaps struct {
    Provider        string
    ModelID         string
    Modalities      []Modality
    ContextWindow   int
    MaxOutput       int
    InputCostPer1K  float64
    OutputCostPer1K float64
    SupportsTools   bool
    SupportsThinking bool
}
```

---

## 三、VLM 视频输入处理流程（含 Qwen）

```
用户构造 ContentPart
         │
         ├─ ContentTypeVideoURL（最优，推荐）
         │   VideoURL: "https://xxx.mp4", VideoFPS: 1.0
         │         │
         │         ├─ QwenProvider    → {"type":"video","video":"url","fps":1.0}  ★ DashScope 服务端采样
         │         ├─ GoogleProvider  → fileData inline/URI（原生视频理解）
         │         └─ OpenAI/Anthropic→ 需先下载拆帧（不支持视频 URL，降级处理）
         │
         ├─ ContentTypeVideoFrame（本地解码后的逐帧 bytes）
         │   []VideoFrame{Data:[]byte, MimeType, TimestampMs}
         │         │
         │         ├─ QwenProvider    → {"type":"video","video":["b64f1","b64f2",...]} ★ 帧数组
         │         ├─ OpenAIProvider  → 多个 image_url content parts
         │         └─ AnthropicProvider→ 多个 image content blocks
         │
         └─ ContentTypeImageURL / ImageData（单图/多图）
                   所有提供商统一处理
```

**VideoDecoder 流程**（video_frame 路径）：
```
视频文件 → FFmpeg/Go image 按帧率采样（默认 1fps）
        → 每帧 JPEG 编码 → []VideoFrame
        → NewVideoFrames() → []ContentPart{Type: video_frame}
```

---

## 三B、Qwen Provider 设计（providers/qwen/）

### DashScope 两种接入模式

| 维度 | 原生 API | OpenAI 兼容接口 |
|------|---------|---------------|
| Base URL | `dashscope.aliyuncs.com/api/v1/...` | `dashscope.aliyuncs.com/compatible-mode/v1` |
| Auth | `Authorization: Bearer <key>` | `Authorization: Bearer <key>` |
| **视频输入** | `{"type":"video","video":"<url>","fps":2.0}` | 同左（Qwen 扩展 `video` type） |
| 多帧序列 | `{"type":"video","video":["frame1",...]}` | 同左 |
| SSE 流式 | `X-DashScope-SSE: enable` 请求头 | 标准 OpenAI SSE |

### Qwen 支持的模型

| Model ID | 视觉 | 视频 | 上下文 |
|----------|------|------|-------|
| `qwen2.5-vl-72b-instruct` | ✅ | ✅ | 128K |
| `qwen2.5-vl-7b-instruct`  | ✅ | ✅ | 128K |
| `qwen2.5-vl-3b-instruct`  | ✅ | ✅ | 32K  |
| `qwen2-vl-72b-instruct`   | ✅ | ✅ | 128K |
| `qwen2-vl-7b-instruct`    | ✅ | ✅ | 32K  |
| `qwen-vl-max`             | ✅ | ❌ | 32K  |
| `qwen-vl-plus`            | ✅ | ❌ | 8K   |

### provider.go + convert.go

```go
// providers/qwen/provider.go
type QwenProvider struct {
    APIKey  string
    BaseURL string  // 默认 "https://dashscope.aliyuncs.com/api/v1"
    client  *http.Client
}

func (p *QwenProvider) Name() string                   { return "qwen" }
func (p *QwenProvider) SupportedModalities() []Modality { return []Modality{Text, Vision, Video} }
func (p *QwenProvider) Stream(ctx, model, messages, opts) (<-chan StreamEvent, error)
```

```go
// providers/qwen/convert.go — 三种视频路径转换
func convertContentParts(parts []ContentPart) []map[string]any {
    switch p.Type {
    case ContentTypeText:      → {"type":"text","text":"..."}
    case ContentTypeImageURL:  → {"type":"image_url","image_url":{"url":"..."}}
    case ContentTypeImageData: → {"type":"image_url","image_url":{"url":"data:...;base64,..."}}
    case ContentTypeVideoURL:  → {"type":"video","video":url,"fps":fps}  ★ 最优
    case ContentTypeVideoFrame:→ 由 GroupVideoFrames() 预处理后合并为帧数组块
    }
}
```

### sse.go（DashScope 原生 SSE 格式）

```
请求头：X-DashScope-SSE: enable
响应格式：
id:1 / event:result
data:{"output":{"choices":[{"delta":{"content":[{"text":"..."}]}}]},"usage":{...}}
```

```go
// providers/qwen/sse.go
func parseDashScopeSSE(r io.Reader, events chan<- StreamEvent)
```

### OpenAI 兼容层 Qwen 扩展（providers/openaicompat/）

```go
type OpenAICompatProvider struct {
    ProviderName  string
    APIKey        string
    BaseURL       string
    QwenVideoMode bool  // true = 启用 Qwen video content type 扩展
}

// 配置示例
NewOpenAICompatProvider("qwen-compat", key,
    "https://dashscope.aliyuncs.com/compatible-mode/v1",
    WithQwenVideoMode(true))

NewOpenAICompatProvider("deepseek", key, "https://api.deepseek.com/v1", nil)
```

```go
// providers/openaicompat/qwen_video.go
// encodeContentForQwen: video_url/video_frame → Qwen video type 扩展格式
func encodeContentForQwen(parts []ContentPart) []map[string]any
```

---

## 四、agentcore 核心类型

```go
// types.go

// AgentMessage — LLM Message 的超集，可含 UI 专属消息
type AgentMessage struct {
    ID        string
    Role      llmprovider.Role
    Content   []llmprovider.ContentPart
    ToolCalls []llmprovider.ToolCall
    Hidden    bool   // true = 不传给 LLM
    Source    string // "bash"|"system"|"ui"
}

// AgentEvent — 事件流（用于 UI 实时更新）
type AgentEventType string

const (
    AgentEventStart         AgentEventType = "agent_start"
    AgentEventTurnStart     AgentEventType = "turn_start"
    AgentEventTextDelta     AgentEventType = "text_delta"
    AgentEventThinkingDelta AgentEventType = "thinking_delta"
    AgentEventToolStart     AgentEventType = "tool_start"
    AgentEventToolUpdate    AgentEventType = "tool_update"
    AgentEventToolEnd       AgentEventType = "tool_end"
    AgentEventTurnEnd       AgentEventType = "turn_end"
    AgentEventEnd           AgentEventType = "agent_end"
)

type AgentEvent struct {
    Type       AgentEventType
    Text       string
    ToolCall   *llmprovider.ToolCall
    ToolResult string
    Usage      *llmprovider.Usage
    Err        error
}

// AgentTool — 工具定义
type AgentTool struct {
    Name          string
    Description   string
    Parameters    json.RawMessage
    Toolset       string
    ExecutionMode string  // "sequential" | "parallel"
    CheckFn       func() bool
    Execute       func(ctx context.Context, args json.RawMessage) (string, error)
    BeforeCall    func(ctx context.Context, call *llmprovider.ToolCall) (bool, error)
    AfterCall     func(ctx context.Context, result string) (string, error)
}
```

---

## 五、Agent Loop

```go
// loop.go

type StreamFn func(ctx context.Context, model string,
    messages []llmprovider.Message, opts llmprovider.StreamOptions) (<-chan llmprovider.StreamEvent, error)

type AgentConfig struct {
    Model         string
    SystemPrompt  string
    Tools         []*AgentTool
    MaxIterations int
    ThinkingLevel string  // "none"|"low"|"medium"|"high"
    StreamFn      StreamFn
}

func RunAgentLoop(
    ctx context.Context,
    config AgentConfig,
    initialMessages []AgentMessage,
    budget *IterationBudget,
    events chan<- AgentEvent,
) ([]AgentMessage, error) {
    messages := initialMessages

    for !budget.Exhausted() {
        select {
        case <-ctx.Done():
            return messages, ctx.Err()
        default:
        }

        // LLM 边界过滤：过滤 Hidden 消息
        llmMessages := ConvertToLLM(messages)

        stream, err := config.StreamFn(ctx, config.Model, llmMessages, buildOpts(config))
        if err != nil { return messages, err }

        response, toolCalls := drainStream(stream, events)
        budget.Spend(1)

        if len(toolCalls) == 0 {
            messages = append(messages, assistantMsg(response))
            break  // 无工具调用 → 正常终止
        }

        results := executeTools(ctx, config.Tools, toolCalls, events)
        messages = append(messages, assistantMsg(response), toolResultMsgs(results)...)
    }
    return messages, nil
}
```

---

## 六、IterationBudget + ToolRegistry

```go
// budget.go — 跨子 Agent 共享预算
type IterationBudget struct {
    mu           sync.Mutex
    remaining    int
    gracePending bool  // 预算耗尽后的 grace call
}

func NewBudget(max int) *IterationBudget
func (b *IterationBudget) Spend(n int)
func (b *IterationBudget) Exhausted() bool
func (b *IterationBudget) TryGraceCall() bool  // 只能用一次

// tool_registry.go — TTL check_fn 缓存
type ToolRegistry struct {
    mu         sync.RWMutex
    tools      map[string]*AgentTool
    generation int64       // 外部可做缓存 key
    cache      checkCache  // check_fn 结果 TTL 缓存 30s
}

const checkFnTTL = 30 * time.Second

func (r *ToolRegistry) Register(t *AgentTool)
func (r *ToolRegistry) Available() []*AgentTool  // 通过 check_fn 的工具
func (r *ToolRegistry) Generation() int64
```

---

## 七、实现顺序（原有）

| 阶段 | 内容 | 文件 |
|------|------|------|
| **P0** | llmprovider 核心类型（含 `ContentTypeVideoURL` + VideoURL 字段） | `types.go` |
| **P0** | Provider 接口 + Registry | `provider.go`, `registry.go` |
| **P0** | OpenAI 提供商（含 VLM 单图/多图） | `providers/openai/` |
| **P0** | Qwen 模型目录 | `providers/qwen/models.go` |
| **P0** | DashScope 原生 Provider + SSE 解析 + convert | `providers/qwen/provider.go`, `sse.go`, `convert.go` |
| **P1** | Anthropic 提供商（含 Thinking） | `providers/anthropic/` |
| **P1** | Google Gemini（含原生视频 URL） | `providers/google/` |
| **P1** | OpenAI-compat 层 + Qwen video 扩展 | `providers/openaicompat/` |
| **P1** | agentcore 类型 + Agent Loop + Budget | `types.go`, `loop.go`, `budget.go` |
| **P1** | ToolRegistry（TTL check_fn） | `tool_registry.go` |
| **P2** | 多帧视频采样解码（VideoDecoder） | `stream.go` 扩展 |
| **P2** | `GroupVideoFrames()` + `NewVideoURL()` 帮助函数 | `types.go` |
| **P2** | Cost 追踪 + 完整模型元数据目录 | `cost.go`, `models.go` |
| **P2** | 单元测试（mock HTTP，验证三种视频路径编码） | `providers/qwen/*_test.go` |

---

## 八、Provider 层强加固（深度分析补充）

> 借鉴来源：Pi `registerApiProvider/sourceId`、OpenCode `Route` 复用、Hermes `ProviderProfile` 声明式设计

### 8.1 分析对比

| 模式 | 来源 | 核心价值 |
|------|------|---------|
| **Route 复用** | OpenCode llm/src/protocols/ | OpenAI-compat provider 只需 5-15 行 `Route.make(...)` 复用同一 Protocol；bug 修复一次传播全部 |
| **sourceId 注册** | Pi `registerApiProvider(provider, sourceId)` | 插件级别 cleanup：`unregisterApiProviders(sourceId)` 一次清除插件注册的所有 API |
| **ProviderProfile 声明式** | Hermes `providers/base.py` | 把 auth_type、fallback_models、health check 开关、aux model 等元数据集中到一个声明式 struct，与运行时客户端解耦 |
| **Live fetch + fallback** | Hermes `fetch_models()` | 先请求 `/models` 接口获取模型列表；失败时回退静态 fallbackModels 列表 |

### 8.2 ProviderProfile（新增，provider.go）

```go
// ProviderProfile — 声明式提供商元数据（不拥有 HTTP client，不负责流式）
// 借鉴 Hermes providers/base.py：将描述与执行分离
type AuthType string

const (
    AuthTypeAPIKey         AuthType = "api_key"
    AuthTypeOAuthDevice    AuthType = "oauth_device_code"
    AuthTypeCopilot        AuthType = "copilot"
    AuthTypeAWSSDK         AuthType = "aws_sdk"
)

type ProviderProfile struct {
    Name               string
    DisplayName        string       // UI 展示名，如 "Alibaba DashScope"
    AuthType           AuthType
    EnvVars            []string     // 候选 API key 环境变量，如 ["DASHSCOPE_API_KEY","QWEN_API_KEY"]
    BaseURL            string
    ModelsURL          string       // 留空则回退 BaseURL+"/models"
    FallbackModels     []string     // live fetch 失败时的静态候选列表
    DefaultHeaders     map[string]string
    SupportsHealthCheck bool
    DefaultAuxModel    string       // 压缩/视觉辅助任务用的轻量模型
    APIMode            string       // "chat_completions"|"responses"|"messages"
}

// FetchModels 调用 ModelsURL 获取实时模型列表；失败时返回 nil（调用方回退 FallbackModels）
func (p *ProviderProfile) FetchModels(ctx context.Context, apiKey string) []string

// ResolveAPIKey 按 EnvVars 顺序查找可用 key
func (p *ProviderProfile) ResolveAPIKey() string
```

### 8.3 Provider 接口扩展（provider.go）

```go
// 原接口新增两个可选方法，通过类型断言调用
type Provider interface {
    Name() string
    SupportedModalities() []Modality
    Stream(ctx context.Context, model string, messages []Message, opts StreamOptions) (<-chan StreamEvent, error)
}

// HealthChecker — 可选接口，Hermes supports_health_check 模式
type HealthChecker interface {
    HealthCheck(ctx context.Context) error
}

// ModelLister — 可选接口，live fetch 后由 Registry 缓存
type ModelLister interface {
    ListModels(ctx context.Context) ([]string, error)
}
```

### 8.4 Registry 强化（registry.go）

```go
// 原 Registry 新增 sourceId 支持，参照 Pi registerApiProvider
type registeredProvider struct {
    provider Provider
    profile  *ProviderProfile  // 可为 nil（兼容旧注册方式）
    sourceId string            // 插件来源标识；空 = 内置
}

type Registry struct {
    mu        sync.RWMutex
    providers map[string]*registeredProvider
    modelCache sync.Map  // key: providerName, value: []string（TTL 5min）
}

func (r *Registry) Register(p Provider, profile *ProviderProfile, sourceId string)
func (r *Registry) Unregister(sourceId string)          // 一次性清除 sourceId 下所有 provider
func (r *Registry) Get(name string) (Provider, bool)
func (r *Registry) GetProfile(name string) (*ProviderProfile, bool)
func (r *Registry) List() []Provider
func (r *Registry) Models(ctx context.Context, name string) []string  // live fetch + fallback
```

### 8.5 OpenAI-compat Route 复用策略

借鉴 OpenCode：`openaicompat/` 目录只存放"路由配置差异"，协议实现在 `providers/openai/` 一处维护。

```
providers/
├── openai/
│   ├── provider.go      # OpenAI 协议实现（chat completions + responses）
│   ├── convert.go       # Message → OpenAI 格式（VLM 单/多图）
│   └── sse.go           # SSE 解析
└── openaicompat/
    ├── registry.go      # 注册所有 compat providers
    ├── profiles.go      # 各 compat provider 的 ProviderProfile 静态声明
    └── provider.go      # OpenAICompatProvider：嵌入 openai.Provider，覆盖 BaseURL/Auth/Headers
```

```go
// openaicompat/provider.go
// OpenAICompatProvider 复用 openai 的 convert/sse，仅差异化 Name()/配置
type OpenAICompatProvider struct {
    openai.Provider               // 嵌入，复用协议实现
    name     string
    baseURL  string
    extraHeaders map[string]string
    qwenVideoMode bool
}

// 工厂函数——5 行即可注册一个新的 compat provider
func RegisterCompat(reg *Registry, profile *ProviderProfile, opts ...Option)
```

**已验证的 compat 列表**（profiles.go 静态声明）：

| 名称 | BaseURL | 特殊处理 |
|------|---------|---------|
| `deepseek` | `api.deepseek.com/v1` | 无 |
| `groq` | `api.groq.com/openai/v1` | 无 |
| `qwen-compat` | `dashscope.aliyuncs.com/compatible-mode/v1` | `qwenVideoMode=true` |
| `together` | `api.together.xyz/v1` | 无 |
| `fireworks` | `api.fireworks.ai/inference/v1` | 无 |

---

## 九、AgentCore 工具链设计（深度分析补充）

> 借鉴来源：Pi `packages/agent/src/types.ts`、`agent-loop.ts`

### 9.1 分析对比

| 模式 | 来源 | 核心价值 |
|------|------|---------|
| **BeforeToolCall / AfterToolCall** | Pi agent types.ts | 在工具执行前后注入钩子，支持拦截（block）、内容替换（AfterToolCallResult.Content）、提前终止（Terminate） |
| **ToolExecutionMode** | Pi agent types.ts | sequential = 串行保证顺序；parallel = 并发执行但结果消息保持源序 |
| **QueueMode** | Pi agent types.ts | 控制 loop 排水点消费用户消息队列的粒度，one-at-a-time 可避免并发输入覆盖 |
| **StreamFn 契约** | Pi agent types.ts | StreamFn 绝不 panic/throw；错误编码进 stream（StopReason="error"），loop 层统一处理 |

### 9.2 BeforeToolCall / AfterToolCall Hooks（types.go 升级）

```go
// hooks.go

// BeforeToolCallResult — 钩子返回值
// Block=true 时工具不执行，loop 注入一条 error tool result；Reason 作为错误文本
type BeforeToolCallResult struct {
    Block  bool
    Reason string // Block=false 时忽略
}

// AfterToolCallResult — 钩子返回值，nil 表示使用原始结果
// 字段级别覆盖（非 nil 才覆盖），不做深度合并
type AfterToolCallResult struct {
    Content   []llmprovider.ContentPart  // 替换工具结果内容
    IsError   *bool                      // 替换 error 标记
    Terminate *bool                      // true = 当前批次所有工具都 Terminate 后停止 loop
}

// BeforeToolCallCtx — 传递给 BeforeToolCall 的上下文
type BeforeToolCallCtx struct {
    AssistantMsg AgentMessage
    ToolCall     llmprovider.ToolCall
    Args         json.RawMessage  // 已通过 schema 校验
    Messages     []AgentMessage   // 当前对话历史（只读）
}

// AfterToolCallCtx — 传递给 AfterToolCall 的上下文
type AfterToolCallCtx struct {
    AssistantMsg AgentMessage
    ToolCall     llmprovider.ToolCall
    Args         json.RawMessage
    Result       string  // 工具原始返回
    IsError      bool
    Messages     []AgentMessage
}

// AgentTool 升级（替换原有 BeforeCall/AfterCall 简单函数）
type AgentTool struct {
    Name          string
    Description   string
    Parameters    json.RawMessage
    Toolset       string
    ExecutionMode ToolExecutionMode
    CheckFn       func() bool           // TTL check，nil = 始终可用
    Execute       func(ctx context.Context, args json.RawMessage) (string, error)
    BeforeCall    func(ctx context.Context, c BeforeToolCallCtx) (BeforeToolCallResult, error)
    AfterCall     func(ctx context.Context, c AfterToolCallCtx) (*AfterToolCallResult, error)
}
```

### 9.3 ToolExecutionMode（loop.go 升级）

```go
// ToolExecutionMode — 工具调用执行模式
// 值定义保持在 types.go；执行逻辑在 loop.go
type ToolExecutionMode string

const (
    // ToolExecSequential: 每个工具完整执行（Before → Execute → After）后再执行下一个
    // 结果消息按源序追加
    ToolExecSequential ToolExecutionMode = "sequential"

    // ToolExecParallel: BeforeCall 顺序执行（保证 block 检查串行），
    // 通过 BeforeCall 的工具并发 Execute，AfterCall 在各自 goroutine 完成后调用，
    // tool_end 事件按完成顺序发送，tool_result 消息追加保持源序
    ToolExecParallel ToolExecutionMode = "parallel"
)

// executeTools — loop.go 内部函数，根据 AgentConfig.ToolExecutionMode 分发
func executeTools(
    ctx context.Context,
    tools []*AgentTool,
    toolCalls []llmprovider.ToolCall,
    mode ToolExecutionMode,
    events chan<- AgentEvent,
) []toolResult
```

### 9.4 QueueMode（loop.go + agent.go 新增）

```go
// QueueMode — 控制 loop 排水点一次消费多少用户消息
type QueueMode string

const (
    // QueueAll: 一次性消费队列所有消息（默认，适合批量任务）
    QueueAll QueueMode = "all"
    // QueueOneAtATime: 每次排水点只消费最旧的一条（适合交互场景，避免输入竞争）
    QueueOneAtATime QueueMode = "one-at-a-time"
)

// AgentConfig 新增字段（loop.go）
type AgentConfig struct {
    Model              string
    SystemPrompt       string
    Tools              []*AgentTool
    MaxIterations      int
    ThinkingLevel      string
    StreamFn           StreamFn
    ToolExecutionMode  ToolExecutionMode  // 默认 ToolExecSequential
    QueueMode          QueueMode          // 默认 QueueAll
}
```

### 9.5 StreamFn 契约（types.go 补充注释 + panic-guard）

```go
// StreamFn — agent loop 使用的流式函数类型
//
// 契约（借鉴 Pi packages/agent/src/types.ts）：
//   1. 永远不 panic，永远不返回 error（错误编码进 channel）
//   2. channel 关闭表示流结束
//   3. 若发生错误，最后一个 StreamEvent 的 Err 字段非 nil，Type="error"
//   4. loop 层检测到 Type="error" 或 StopReason="aborted" 后走统一错误路径
type StreamFn func(
    ctx context.Context,
    model string,
    messages []llmprovider.Message,
    opts llmprovider.StreamOptions,
) <-chan llmprovider.StreamEvent

// WrapStreamFn 将 Provider.Stream 包装为满足 StreamFn 契约的函数
// 捕获 Provider.Stream 返回的 error，注入为 StreamEvent{Type: StreamEventError, Err: err}
func WrapStreamFn(p llmprovider.Provider) StreamFn
```

---

## 十、更新后完整实现顺序

| 阶段 | 内容 | 文件 | 来源参考 |
|------|------|------|---------|
| **P0** | llmprovider 核心类型（ContentPart / VLM / VideoURL） | `types.go` | — |
| **P0** | `ProviderProfile` 声明式结构 | `provider.go` | Hermes base.py |
| **P0** | Provider 接口（含 HealthChecker/ModelLister 可选） | `provider.go` | Hermes/Pi |
| **P0** | Registry（sourceId 注册 + Unregister + Models fetch） | `registry.go` | Pi api-registry |
| **P0** | OpenAI 提供商（VLM 单/多图） | `providers/openai/` | Pi openai-completions |
| **P0** | Qwen 原生 Provider + SSE + convert | `providers/qwen/` | — |
| **P1** | Anthropic 提供商（含 Thinking） | `providers/anthropic/` | Pi anthropic.ts |
| **P1** | Google Gemini（含原生视频 URL） | `providers/google/` | Pi google.ts |
| **P1** | OpenAI-compat 层（profiles.go + provider.go） | `providers/openaicompat/` | OpenCode protocols/ |
| **P1** | `WrapStreamFn` + StreamFn 契约封装 | `agentcore/stream_fn.go` | Pi types.ts |
| **P1** | hooks.go（BeforeToolCallCtx/AfterToolCallCtx/Result） | `agentcore/hooks.go` | Pi types.ts |
| **P1** | agentcore types + loop（ToolExecutionMode/QueueMode） | `types.go`, `loop.go` | Pi agent-loop.ts |
| **P1** | AgentTool 升级（BeforeCall/AfterCall 新签名） | `types.go` | Pi types.ts |
| **P1** | Budget + ToolRegistry（TTL check_fn） | `budget.go`, `tool_registry.go` | Pi/Hermes |
| **P2** | 多帧视频采样解码（VideoDecoder） | `stream.go` 扩展 | — |
| **P2** | `GroupVideoFrames()` / `NewVideoURL()` | `types.go` | — |
| **P2** | Cost 追踪 + 完整模型元数据目录 | `cost.go`, `models.go` | Pi models.generated |
| **P2** | 并行工具执行（ToolExecParallel goroutine fan-out） | `loop.go` | Pi agent-loop.ts |
| **P2** | ProviderProfile.FetchModels + Registry.Models 缓存 | `registry.go`, `provider.go` | Hermes fetch_models |
| **P2** | 单元测试（mock HTTP，三种视频路径 + hook 拦截） | `providers/qwen/*_test.go`, `agentcore/*_test.go` | — |

---

## 十二、llmprovider 代码级深化补充

> 来源：Pi `types.ts` StreamOptions + `event-stream.ts` + `register-builtins.ts`；OpenCode `schema/events.ts`

### 12.1 StreamOptions 扩展

Pi 的 `StreamOptions` 远比现有计划丰富。Go 侧 `StreamOptions` 需补充：

```go
// StreamOptions — 流式请求通用选项（provider 按需解读，不识别的字段忽略）
type StreamOptions struct {
    APIKey    string
    Signal    context.Context  // 用 ctx 代替 AbortSignal
    Reasoning ThinkingLevel

    // ── 请求生命周期钩子（借鉴 Pi StreamOptions.onPayload/onResponse）──
    // OnPayload: 在 HTTP body 发送前被调用；返回 nil 则使用原始 payload；
    // 用于调试拦截、灰度替换、测试注入
    OnPayload func(payload []byte, model string) []byte
    // OnResponse: HTTP 响应头到达后、body stream 消费前被调用；用于日志/指标
    OnResponse func(statusCode int, headers http.Header, model string)

    // ── 请求级元数据（借鉴 Pi metadata/sessionId）──
    // SessionId: provider 侧 session caching hint（如 Anthropic session cache）
    SessionId string
    // Metadata: provider 提取感兴趣的字段；如 Anthropic 使用 user_id 反滥用
    Metadata map[string]any

    // ── 超时与重试（借鉴 Pi timeoutMs/maxRetries/maxRetryDelayMs）──
    TimeoutMs       int // 0 = SDK 默认（通常 10 分钟）
    MaxRetries      int // 0 = SDK 默认（通常 2）
    // MaxRetryDelayMs: 服务端要求等待超此值则立即失败（交给上层处理）
    // 0 = 不限制；默认 60000
    MaxRetryDelayMs int

    // 额外自定义 headers（合并 provider 默认 headers，可覆盖）
    Headers map[string]string
    // 额外 body 字段（provider 特有扩展，如 DashScope enable_search）
    ExtraBody map[string]any
}
```

### 12.2 Usage 非重叠分解设计

借鉴 OpenCode `schema/events.ts` Usage 设计，消除 "双减" 型 bug：

```go
// Usage — token 用量。两套字段相互独立，永远不需要相减。
//
// 不变量（借鉴 OpenCode）:
//   NonCachedInput + CacheReadInput + CacheWriteInput = Input (provider 保证)
//   ReasoningOutput ≤ Output
//   VisibleOutput = max(0, Output - ReasoningOutput)
type Usage struct {
    // 包容性合计（OpenAI/Gemini 风格 — 含缓存部分）
    Input  int64
    Output int64
    Total  int64  // provider 上报，或 Input+Output

    // 非重叠分解（独立有意义，消费者按需读取，无需相减）
    NonCachedInput  int64  // 非缓存命中的新 prompt tokens
    CacheReadInput  int64  // 从缓存读取的 input tokens
    CacheWriteInput int64  // 写入缓存的 input tokens
    ReasoningOutput int64  // output 中用于思考的 tokens（Anthropic 未分离，保持 0）

    Cost Cost
}

func (u Usage) VisibleOutput() int64 {
    v := u.Output - u.ReasoningOutput
    if v < 0 { return 0 }
    return v
}
```

### 12.3 StreamHandle — Pi EventStream 的 Go 等价物

Pi 的 `EventStream<T,R>` 支持：1) `push(event)` 逐个推送；2) `end(result)` 发送最终值；3) `result()` 返回 Promise。  
Go 等价物用结构体封装 channel + 一次性结果 channel：

```go
// StreamHandle — llmprovider 流式响应句柄
// 借鉴 Pi EventStream<AssistantMessageEvent, AssistantMessage>
type StreamHandle struct {
    Events <-chan StreamEvent  // 消费者读取
    done   chan struct{}
    result chan StreamResult
}

type StreamResult struct {
    Usage    Usage
    StopReason string
    Err      error
}

// Result 阻塞直到流结束，返回最终聚合结果（类比 Pi stream.result()）
func (h *StreamHandle) Result(ctx context.Context) (StreamResult, error)

// 内部构造（provider 侧使用）
type streamProducer struct {
    events chan<- StreamEvent
    result chan<- StreamResult
}
func (p *streamProducer) Push(e StreamEvent)
func (p *streamProducer) End(r StreamResult)
func newStreamPair() (*StreamHandle, *streamProducer)
```

### 12.4 惰性加载注册机制（Pi register-builtins 模式）

Pi 的 `register-builtins.ts` 使用接口包装延迟 import，避免所有 provider 在 package 加载时全部初始化。Go 等价物：

```go
// providers/register.go — 惰性注册入口
// 只 import provider 的 profile（轻量），不 import 其 HTTP 实现
// provider 的 init() 函数负责向 GlobalRegistry 注册

// providers/openai/provider.go
func init() {
    llmprovider.GlobalRegistry.Register(&OpenAIProvider{}, &OpenAIProfile, "builtin")
}

// main.go / app.go — 用 blank import 触发 init()
import (
    _ "backend/llmprovider/providers/openai"
    _ "backend/llmprovider/providers/anthropic"
    _ "backend/llmprovider/providers/google"
    _ "backend/llmprovider/providers/qwen"
    _ "backend/llmprovider/providers/openaicompat"
)
// 插件 provider: 运行时 plugin.Open() + Register()，用 sourceId 标记，可 Unregister
```

---

## 十三、agentcore 代码级深化补充

> 来源：Pi `agent-loop.ts`（全文）+ `types.ts`（AgentLoopConfig 全字段）+ Hermes `iteration_budget.py` + OpenCode `tool-runtime.ts`

### 13.1 AgentLoopConfig 完整回调集

现有计划的 `AgentConfig` 缺少 Pi 中最关键的回调机制，补充如下：

```go
// AgentLoopConfig — loop 完整配置（借鉴 Pi AgentLoopConfig extends SimpleStreamOptions）
type AgentLoopConfig struct {
    Model         string
    SystemPrompt  string
    Tools         []*AgentTool
    MaxIterations int
    ThinkingLevel ThinkingLevel
    StreamFn      StreamFn
    ToolExecution ToolExecutionMode  // 默认 parallel（同 Pi 默认值！）
    QueueMode     QueueMode

    // ── 必填回调 ──
    // ConvertToLlm: AgentMessage[] → llmprovider.Message[]（借鉴 Pi）
    // 合约：永不 panic；过滤掉无法转换的自定义消息类型；返回空切片为合法值
    ConvertToLlm func(messages []AgentMessage) ([]llmprovider.Message, error)

    // ── 可选回调（全部借鉴 Pi AgentLoopConfig）──

    // TransformContext: ConvertToLlm 前的 AgentMessage 级变换
    // 用途：上下文窗口裁剪、外部记忆注入
    // 合约：永不 panic；失败返回原始 messages
    TransformContext func(ctx context.Context, msgs []AgentMessage) ([]AgentMessage, error)

    // GetApiKey: 每次 LLM 调用前动态解析 API key（用于 OAuth 短期 token）
    // 合约：永不 panic；无 key 时返回 ("", nil)
    GetApiKey func(ctx context.Context, provider string) (string, error)

    // ShouldStopAfterTurn: turn_end 后调用；返回 true → 跳过排水点直接 agent_end
    // 合约：永不 panic
    ShouldStopAfterTurn func(ctx AfterTurnCtx) bool

    // PrepareNextTurn: turn_end 后、下一次 LLM 调用前；
    // 返回 nil 保持当前 config；否则 loop 用返回值替换 model/context/thinkingLevel
    PrepareNextTurn func(ctx AfterTurnCtx) *AgentTurnUpdate

    // GetSteeringMessages: 工具执行完毕后调用；返回的消息在下次 LLM 前注入
    // 用途：用户中途干预、动态 system prompt 修改
    // 合约：永不 panic；无消息时返回 nil
    GetSteeringMessages func(ctx context.Context) []AgentMessage

    // GetFollowUpMessages: agent 即将停止时调用；返回消息则继续 outer loop
    // 用途：实现 one-at-a-time 队列排水
    // 合约：永不 panic；无消息时返回 nil
    GetFollowUpMessages func(ctx context.Context) []AgentMessage
}

// AgentTurnUpdate — PrepareNextTurn 的返回值（借鉴 Pi AgentLoopTurnUpdate）
type AgentTurnUpdate struct {
    Context      *AgentContext  // nil = 保持原有 context
    Model        string          // "" = 保持原有 model
    ThinkingLevel ThinkingLevel  // "" = 保持原有级别
}

// AfterTurnCtx — ShouldStopAfterTurn / PrepareNextTurn 的入参
type AfterTurnCtx struct {
    Message     AgentMessage
    ToolResults []AgentMessage
    Context     AgentContext
    NewMessages []AgentMessage
}
```

### 13.2 双层 Loop 结构

借鉴 Pi `runLoop()` 的外层（follow-up）+ 内层（tool calls + steering）双循环：

```go
// loop.go — runLoop 结构（伪代码）
func runLoop(ctx, initialCtx, config, emit) error {
    currentCtx := initialCtx
    firstTurn := true
    pendingMsgs := config.GetSteeringMessages(ctx)  // 启动时检查

    // ── 外层 loop：follow-up 消息驱动 ──
    for {
        hasMoreToolCalls := true

        // ── 内层 loop：工具调用 + steering ──
        for hasMoreToolCalls || len(pendingMsgs) > 0 {
            if !firstTurn { emit(TurnStart) } else { firstTurn = false }

            // 注入 pending steering messages
            for _, m := range pendingMsgs { currentCtx.Messages = append(currentCtx.Messages, m) }
            pendingMsgs = nil

            // LLM 调用
            msg := streamAssistantResponse(ctx, currentCtx, config, emit)
            if msg.StopReason == "error" || msg.StopReason == "aborted" {
                emit(TurnEnd); emit(AgentEnd); return nil
            }

            // 工具执行
            toolResults, terminate := executeToolCalls(ctx, currentCtx, msg, config, emit)
            hasMoreToolCalls = !terminate
            currentCtx.Messages = append(currentCtx.Messages, toolResults...)

            emit(TurnEnd{msg, toolResults})

            // prepareNextTurn hook
            if upd := config.PrepareNextTurn(AfterTurnCtx{...}); upd != nil {
                // 更新 model/context/thinkingLevel
            }

            // shouldStopAfterTurn hook
            if config.ShouldStopAfterTurn(AfterTurnCtx{...}) {
                emit(AgentEnd); return nil
            }

            pendingMsgs = config.GetSteeringMessages(ctx)
        }

        // ── 外层：agent 即将停止，检查 follow-up ──
        followUp := config.GetFollowUpMessages(ctx)
        if len(followUp) == 0 { break }
        pendingMsgs = followUp  // 重新进入内层 loop
    }
    emit(AgentEnd)
}
```

### 13.3 工具执行细节补充

**hasSequentialToolCall 覆盖逻辑**（借鉴 Pi）：

```go
func pickExecutionMode(tools []*AgentTool, toolCalls []ToolCall, defaultMode ToolExecutionMode) ToolExecutionMode {
    // Pi 规则：只要 batch 中任意一个 tool 的 executionMode == sequential，
    // 整个 batch 退化为 sequential
    for _, tc := range toolCalls {
        for _, t := range tools {
            if t.Name == tc.Name && t.ExecutionMode == ToolExecSequential {
                return ToolExecSequential
            }
        }
    }
    return defaultMode
}
```

**AgentTool 补充 PrepareArgs shim**（借鉴 Pi `prepareArguments`）：

```go
type AgentTool struct {
    // ... 现有字段 ...

    // PrepareArgs: schema 校验前的原始参数兼容 shim
    // 用途：provider 返回轻微格式差异（如 string vs int）时的透明适配
    // 合约：返回值必须能通过 Parameters schema 校验
    PrepareArgs func(raw json.RawMessage) json.RawMessage

    ExecutionMode ToolExecutionMode  // 单个工具级别覆盖；空 = 继承 config 默认
}
```

**tool_execution_update 流式事件**（借鉴 Pi `onUpdate` callback）：

```go
// AgentTool.Execute 支持流式进度回调
type ToolUpdateCallback func(partial ToolResult)

// Execute 签名更新
Execute func(
    ctx     context.Context,
    id      string,
    args    json.RawMessage,
    onUpdate ToolUpdateCallback,  // nil-safe；provider 按需调用
) (ToolResult, error)

// AgentEvent 新增
| { type: "tool_execution_update"; ToolCallId string; PartialResult ToolResult }
```

**shouldTerminateToolBatch 语义**（借鉴 Pi）：

```go
// terminate 只有当 batch 中 ALL 工具的 ToolResult.Terminate == true 时才生效
func shouldTerminateToolBatch(results []ToolResult) bool {
    if len(results) == 0 { return false }
    for _, r := range results {
        if !r.Terminate { return false }
    }
    return true
}
```

### 13.4 Budget.Refund() + 子 Agent 独立 Budget

借鉴 Hermes `IterationBudget`：

```go
// budget.go
type Budget struct {
    maxTotal int
    used     int64
    mu       sync.Mutex
}

func NewBudget(max int) *Budget { return &Budget{maxTotal: max} }

// Consume 消费一次；返回 false 则不允许继续（budget 耗尽）
func (b *Budget) Consume() bool {
    b.mu.Lock(); defer b.mu.Unlock()
    if int(b.used) >= b.maxTotal { return false }
    b.used++; return true
}

// Refund 退还一次（用于工具辅助调用等不计 LLM iteration 的操作）
// 借鉴 Hermes IterationBudget.refund()
func (b *Budget) Refund() {
    b.mu.Lock(); defer b.mu.Unlock()
    if b.used > 0 { b.used-- }
}

func (b *Budget) Remaining() int { ... }
func (b *Budget) Used() int      { ... }

// 子 Agent 各自持有独立 Budget（不共享 parent）
// parent max=90, subagent max=50（同 Hermes 默认值）
```

### 13.5 Result 类型（期望失败值化）

借鉴 Pi harness `types.ts` 的 `Result<T,E>` 模式，Go 等价：

```go
// result.go — 期望失败用 Result 返回，不用 panic
// 用于 BeforeToolCallResult、转换操作等可预期失败场景
type Result[T any] struct {
    Value T
    Err   error
    ok    bool
}

func OK[T any](v T) Result[T]  { return Result[T]{Value: v, ok: true} }
func Err[T any](e error) Result[T] { return Result[T]{Err: e} }
func (r Result[T]) IsOk() bool { return r.ok }
func (r Result[T]) Unwrap() (T, error) { return r.Value, r.Err }
```

---

## 十一、四个开源项目架构对比摘要

> 本节记录深度分析结论，供后续模块（会话管理/上下文管理）参考

### llmprovider 层对比

| 维度 | Pi (packages/ai) | OpenCode (packages/llm) | Hermes (providers/) | Claude Code |
|------|-----------------|------------------------|---------------------|-------------|
| **注册机制** | `registerApiProvider(api, sourceId)` Map；可按 sourceId unregister | 静态 `Route.make(...)` + 全局 route 数组 | 三级懒发现：bundled→user→legacy | 无显式注册；静态导入 |
| **Provider 抽象** | `ApiProvider<TApi, TOptions>` 泛型；stream / streamSimple 双入口 | `Route` = Protocol+Endpoint+Auth+Framing 四轴 | `ProviderProfile` 声明式 dataclass | 大型 `query.ts` 单文件 |
| **Auth** | env-api-keys → auth.json → OAuth 三级链 | `Auth.bearer()` / `Auth.config(envVar)` 构造时绑定 | `auth_type` 枚举字段 + `ResolveAPIKey()` | Hardcoded client 构造 |
| **模型元数据** | `models.generated.ts` 代码生成（脚本维护） | `Model` Schema 类；catalog 在 opencode/core | `fallback_models` 静态列表 + `fetch_models()` | 内联常量 |
| **Route 复用** | `streamSimple` 统一 SimpleStreamOptions | OpenAI-compat 共享 `OpenAIChat.protocol` | `api_mode` 枚举 + 同一 openai 客户端 | 无 |

### agentcore 层对比

| 维度 | Pi (packages/agent) | Hermes (run_agent.py) | OpenCode (opencode/session) | Claude Code |
|------|--------------------|-----------------------|-----------------------------|-------------|
| **Loop 结构** | `agentLoop()` 返回 `EventStream<AgentEvent, AgentMessage[]>`；async 生产者-消费者 | `while iterations < max` 同步循环；`_budget_grace_call` 一次 grace | Effect-based stream；`session/llm.ts` 决策 AI SDK vs native route | `QueryEngine.ts` + 递归 `query.ts` |
| **工具执行** | `ToolExecutionMode`: sequential/parallel；`BeforeCall/AfterCall` hooks；`Terminate` hint | `handle_function_call()` 串行；`parallel_tool_calls` 参数 | Effect `toolExecution: "none"\|"all"\|"required"` | Tool.ts 注册；串行执行 |
| **预算控制** | 无显式 budget；MaxIterations 在 config | `IterationBudget` 跨子 Agent 共享；grace call 机制 | 无显式；MaxSteps | 无显式 |
| **消息队列** | `QueueMode`: all / one-at-a-time | 单线程顺序消费；`prefill_messages` 预填 | 无 queue；单消息进 session | 无 queue |
| **错误处理** | StreamFn 永不 throw；`StopReason:"error"\|"aborted"` 编码进 stream | try/except 包裹；fallback model 切换 | `LLMError` Effect 类型化错误 | Error catch + retry |
| **上下文管理** | `harness/compaction/` — `shouldCompact()` + `compact()`；split-turn；`CompactionSettings` 三参数；`session_before_compact` hook 可拦截 | `ContextEngine` ABC 可插拔；默认 `ContextCompressor`（head/tail 保护、tool pruning、scaled summary）；`threshold_percent` 触发 | `Compaction.Started/Delta/Ended` 事件驱动；Adapter 模式更新消息状态 | `query.ts` 内联五层：toolResultBudget→snip→micro→collapse→auto/reactive；`autoCompact.ts` 阈值 |
| **会话管理** | `harness/session/` — tree-based `Session` + `SessionStorage` 接口；`InMemory`/`Jsonl` 双实现；`SessionRepo` CRUD+fork；entry=message\|compaction\|branch_summary\|model_change | `gateway/session.py` — `SessionSource`+`SessionContext` dataclass；disk JSON 持久化；`SessionResetPolicy` 控制重置 | `Session.ID` branded type；`SessionMessage` Effect Schema；event-sourcing（事件→状态投影） | `QueryEngine.mutableMessages` 数组；`recordTranscript()` fire-and-forget；无独立 session 抽象 |

---

## 十二、四个开源项目上下文管理/会话管理深度对比

> 本节对 Pi、Hermes、Claude Code、OpenCode 的上下文压缩和会话持久化架构做深度分析，为 agentcore 子模块设计提供依据。

### 12.1 上下文管理（Context Compaction）对比

#### Pi — `harness/compaction/compaction.ts`

**触发策略**：`shouldCompact(contextTokens, contextWindow, settings)` — 当 `contextTokens > contextWindow - reserveTokens` 时触发。Token 估算采用双轨方式：优先使用最近一次 assistant response 的 `usage` 字段（精确值），再加上 usage 之后新消息的字符启发式估算（chars/4）。

**压缩算法**：
- `prepareCompaction()` 遍历 `SessionTreeEntry[]`，找到合法切点（user/assistant/custom 消息边界，排除 toolResult），按 `keepRecentTokens` 保留尾部
- 支持 **split-turn**：当最佳切点落在一个大 turn 中间时，将该 turn 拆分为 prefix（压缩）+ suffix（保留），两段各自生成 summary
- `compact()` 调用 LLM 生成摘要，追加 `formatFileOperations()` 文件操作记录（readFiles / modifiedFiles）
- 压缩结果写入 `CompactionEntry{summary, firstKeptEntryId, tokensBefore}`

**可插拔性**：通过 `session_before_compact` hook 事件，上层可拦截/替换/取消压缩（返回 `{cancel: true}` 或 `{compaction: CompactResult}`）。

**配置**：`CompactionSettings{enabled, reserveTokens, keepRecentTokens}` — 三个参数控制全部行为。

#### Hermes — `agent/context_engine.py` + `agent/context_compressor.py`

**触发策略**：`ContextEngine` 抽象基类定义 `should_compress(prompt_tokens)` + `should_compress_preflight(messages)` 双阶段检查。默认 `threshold_percent=0.75`，即上下文使用超过 75% 时触发。

**压缩算法**（默认 `ContextCompressor`）：
- **Head/Tail 保护**：`protect_first_n=3`（非 system 头消息）+ `protect_last_n=6`（尾部消息），中间段压缩
- **Tool output pruning**：压缩前预处理，旧 tool_result 替换为 `[Old tool output cleared to save context space]`，截断长 tool_call arguments JSON（保持 JSON 合法性）
- **Image 剥离**：旧 turn 的 `image_url`/`input_image` 替换为 `[screenshot removed to save context]`
- **Scaled summary budget**：摘要 token 预算 = min(压缩内容 × 20%, 12000)，下限 2000
- **Iterative summary**：多次压缩时将前一次 summary 作为 `previousSummary` 传入，保持信息连续性
- **结构化 summary**：模板包含 Resolved/Pending 问题追踪、Active Task 标记

**可插拔性**：`ContextEngine` ABC 允许完全替换引擎（如 LCM 引擎）。引擎还可暴露自己的 tool（`get_tool_schemas()` / `handle_tool_call()`）。

#### Claude Code — `query.ts` + `services/compact/autoCompact.ts`

**触发策略**：`getAutoCompactThreshold(model)` = `effectiveContextWindow - AUTOCOMPACT_BUFFER_TOKENS(13000)`。每个 turn 结束后 `isAutoCompactEnabled()` 检查。

**压缩算法** — 五层 pipeline：
1. **toolResultBudget**：聚合 tool result 总量，超限时持久化到磁盘、用摘要替代
2. **snipCompact**：裁剪超大单条消息
3. **microCompact**：轻量级中间消息清理（不调用 LLM）
4. **contextCollapse**：折叠搜索/读取类操作的详细输出
5. **autoCompact / reactiveCompact**：调用 LLM 生成摘要（autoCompact 主动触发，reactiveCompact 在 prompt-too-long 错误后被动触发）

**特殊机制**：`MAX_CONSECUTIVE_AUTOCOMPACT_FAILURES=3` 熔断器；`reactiveCompact` 在 prompt-too-long 时立即触发而非等到 turn 结束。

#### OpenCode — `session-event.ts` + `session-message-updater.ts`

**触发策略**：事件驱动 — `Compaction.Started` 事件由上层 session 逻辑触发。

**压缩算法**：`Compaction.Delta` 流式接收摘要文本，`Compaction.Ended` 写入最终 summary + include 字段。`session-message-updater.ts` 通过 `Adapter` 模式将 compaction 事件投影为 `SessionMessage.Compaction` 消息。

**特点**：压缩过程本身是一等事件流公民，与 assistant/tool 事件同等处理，利于 UI 实时展示压缩进度。

#### Go 借鉴要点

| 维度 | 采纳方案 | 来源 |
|------|---------|------|
| **可插拔引擎** | `ContextEngine` 接口，默认实现可替换 | Hermes ABC |
| **双轨 token 估算** | usage 精确值 + 字符启发式补充 | Pi `estimateContextTokens` |
| **Head/Tail 保护** | `ProtectFirstN` + `ProtectLastN` 配置 | Hermes compressor |
| **Tool pruning 预处理** | 压缩前先清理旧 tool output + 截断 args | Hermes `_PRUNED_TOOL_PLACEHOLDER` |
| **Split-turn** | 大 turn 拆分压缩 | Pi `isSplitTurn` |
| **压缩事件流** | 压缩过程通过 `AgentEvent` 流式通知上层 | OpenCode `Compaction.*` |
| **熔断器** | 连续失败 N 次后停止重试 | Claude Code `MAX_CONSECUTIVE_AUTOCOMPACT_FAILURES` |
| **文件操作追踪** | 摘要附带 readFiles/modifiedFiles | Pi `computeFileLists` |

### 12.2 会话管理（Session）对比

#### Pi — `harness/session/`

**存储模型**：Tree-based — 每条 `SessionTreeEntry` 有 `{id, parentId, timestamp, type}`，通过 `parentId` 形成链表/树结构。支持 10 种 entry type：`message | compaction | branch_summary | model_change | thinking_level_change | custom | custom_message | label | session_info | leaf`。

**持久化**：
- `SessionStorage` 接口定义 `appendEntry()` / `getEntry()` / `getPathToRoot()` / `getLeafId()` / `setLeafId()`
- `InMemorySessionStorage`：Map-based，用于测试和短期会话
- `JsonlSessionStorage`：每条 entry 追加写入 `.jsonl` 文件（一行一个 JSON），读取时逐行解析

**恢复**：`buildSessionContext(pathEntries)` — 从 leaf 沿 parentId 回溯到 root，收集所有 entry，遇到 `compaction` entry 时跳过被压缩的消息段（从 `firstKeptEntryId` 开始保留），重建 `SessionContext{messages, thinkingLevel, model}`。

**Fork**：`SessionRepo.fork(source, {entryId, position})` — 复制 source 到 entryId 为止的 entries 到新 session，支持 `before` / `at` 两种切点语义。

**会话仓库**：`SessionRepo` 接口统一 `create()` / `open()` / `list()` / `delete()` / `fork()`。`JsonlSessionRepo` 按 CWD 分目录存储，文件名含时间戳和 session ID。

#### Hermes — `gateway/session.py`

**存储模型**：Flat — `SessionSource` dataclass 描述消息来源（platform/chat_id/user_id/thread_id），`SessionContext` 聚合来源 + 连接平台 + home channels。消息本身以 OpenAI 格式 `List[Dict]` 内存持有。

**持久化**：原子 JSON 文件写入（`atomic_replace`），按 session_key 索引。

**重置策略**：`SessionResetPolicy` 控制何时清空会话（超时、命令触发、平台切换）。

**特点**：面向多平台网关（Telegram/Discord/WhatsApp/Signal），session 是以 chat 维度组织的，不是以用户维度。`build_session_context_prompt()` 将 session 上下文注入 system prompt。

#### Claude Code — `QueryEngine.ts`

**存储模型**：`mutableMessages: Message[]` — 简单可变数组，无 tree 结构。

**持久化**：`recordTranscript(messages)` fire-and-forget 写入（assistant 消息不 await，其他消息 await）。100ms lazy jsonStringify 的写队列。

**恢复**：通过 `ask()` 函数传入克隆的 messages 数组。无显式 session 抽象。

#### OpenCode — `packages/core/src/session*`

**存储模型**：Event-sourcing — 所有状态变更（prompted/step.started/tool.called/compaction.started 等 20+ 事件类型）作为不可变事件存储。`session-message-updater.ts` 将事件序列投影为 `SessionMessage.Message[]` 状态。

**消息类型**：`User | Synthetic | Shell | Assistant | Compaction | AgentSwitched | ModelSwitched` — 7 种，使用 Effect Schema 强类型定义。

**Adapter 模式**：`SessionMessageUpdater.Adapter<Result>` 接口抽象了状态更新操作（`getCurrentAssistant()` / `updateAssistant()` / `appendMessage()` / `finish()`），允许 memory / database / 其他后端无缝切换。

#### Go 借鉴要点

| 维度 | 采纳方案 | 来源 |
|------|---------|------|
| **Tree-based entry 模型** | `SessionEntry{ID, ParentID, Timestamp, Type, Payload}` 链式结构 | Pi `SessionTreeEntry` |
| **SessionStorage 接口** | `AppendEntry / GetEntry / GetPathToRoot / GetLeafID` | Pi `SessionStorage` |
| **双实现** | `InMemoryStorage`（测试）+ `JsonlStorage`（生产） | Pi 双实现 |
| **SessionRepo CRUD+Fork** | `Create / Open / List / Delete / Fork` 统一接口 | Pi `SessionRepo` |
| **BuildContext 重建** | 从 leaf 回溯重建 messages + model + thinkingLevel，跳过被压缩段 | Pi `buildSessionContext` |
| **Entry 类型丰富** | message / compaction / model_change / branch_summary / custom | Pi 10 种 entry type |
| **SessionContext 注入** | 会话上下文生成 system prompt 片段 | Hermes `build_session_context_prompt` |

---

## 十三、agentcore 上下文管理子模块设计

### 13.1 核心类型

```go
// agentcore/context/types.go

// CompactionSettings 控制压缩行为
type CompactionSettings struct {
    Enabled         bool
    ReserveTokens   int    // 预留给输出的 token 数（默认 20000）
    KeepRecentTokens int   // 尾部保护 token 数
    ProtectFirstN   int    // 头部保护非 system 消息数（默认 3，借鉴 Hermes）
    ProtectLastN    int    // 尾部保护消息数（默认 6，借鉴 Hermes）
    ThresholdPercent float64 // 触发阈值百分比（默认 0.75，借鉴 Hermes）
    MaxConsecutiveFailures int // 熔断器：连续失败次数上限（默认 3，借鉴 Claude Code）
}

// DefaultCompactionSettings 返回默认配置
func DefaultCompactionSettings() CompactionSettings {
    return CompactionSettings{
        Enabled:          true,
        ReserveTokens:    20000,
        KeepRecentTokens: 8000,
        ProtectFirstN:    3,
        ProtectLastN:     6,
        ThresholdPercent:  0.75,
        MaxConsecutiveFailures: 3,
    }
}

// ContextEstimate 双轨 token 估算结果（借鉴 Pi estimateContextTokens）
type ContextEstimate struct {
    Total         int // 估算总 token 数
    UsageTokens   int // 来自 API usage 的精确值
    TrailingTokens int // usage 之后新消息的启发式估算
    LastUsageIndex int // 最近 usage 所在消息索引（-1 表示无）
}

// CompactionResult 压缩执行结果
type CompactionResult struct {
    Summary          string
    FirstKeptEntryID string   // 压缩后保留的第一条 entry ID
    TokensBefore     int      // 压缩前的 token 数
    ReadFiles        []string // 摘要中涉及的读取文件（借鉴 Pi）
    ModifiedFiles    []string // 摘要中涉及的修改文件（借鉴 Pi）
}

// CompactionPreparation 压缩准备数据（借鉴 Pi prepareCompaction）
type CompactionPreparation struct {
    FirstKeptEntryID    string
    MessagesToSummarize []AgentMessage
    TurnPrefixMessages  []AgentMessage // split-turn 前缀（借鉴 Pi）
    IsSplitTurn         bool
    TokensBefore        int
    PreviousSummary     string // 迭代摘要用（借鉴 Hermes iterative summary）
    FileOps             FileOperations
    Settings            CompactionSettings
}

// FileOperations 文件操作追踪（借鉴 Pi）
type FileOperations struct {
    Read     map[string]struct{}
    Written  map[string]struct{}
    Edited   map[string]struct{}
}
```

### 13.2 ContextEngine 接口

```go
// agentcore/context/engine.go

// ContextEngine 可插拔的上下文管理引擎（借鉴 Hermes ABC）
type ContextEngine interface {
    // Name 返回引擎标识（如 "compactor", "lcm"）
    Name() string

    // UpdateFromResponse 从 LLM 响应更新 token 跟踪
    UpdateFromResponse(usage llmprovider.Usage)

    // ShouldCompact 判断是否需要触发压缩
    ShouldCompact() bool

    // ShouldCompactPreflight 快速预检（API 调用前，可选）
    ShouldCompactPreflight(messages []AgentMessage) bool

    // Prepare 准备压缩数据（选择切点、分割消息）
    Prepare(entries []session.SessionEntry, settings CompactionSettings) (*CompactionPreparation, error)

    // Compact 执行压缩（调用 LLM 生成摘要）
    Compact(ctx context.Context, prep *CompactionPreparation) (*CompactionResult, error)

    // OnSessionStart 会话开始回调
    OnSessionStart(sessionID string)

    // OnSessionEnd 会话结束回调
    OnSessionEnd(sessionID string)

    // OnSessionReset 会话重置回调
    OnSessionReset()

    // GetStatus 返回状态信息（用于 UI 展示）
    GetStatus() ContextStatus
}

// ContextStatus 引擎状态（借鉴 Hermes get_status）
type ContextStatus struct {
    LastPromptTokens int
    ThresholdTokens  int
    ContextLength    int
    UsagePercent     float64
    CompressionCount int
}

// CompactFn 压缩函数签名（注入到 AgentLoopConfig，类似 StreamFn）
// 接收消息列表，返回压缩后的消息列表 + 压缩结果
type CompactFn func(ctx context.Context, messages []AgentMessage) ([]AgentMessage, *CompactionResult, error)
```

### 13.3 默认 Compactor 实现

```go
// agentcore/context/compactor.go

// Compactor 默认压缩引擎实现
type Compactor struct {
    settings         CompactionSettings
    contextLength    int     // 模型上下文窗口大小
    lastPromptTokens int
    lastCompTokens   int
    thresholdTokens  int
    compressionCount int
    consecutiveFailures int  // 熔断计数器（借鉴 Claude Code）
    summarizeFn      SummarizeFn // LLM 摘要调用（DI 注入）
}

// SummarizeFn 摘要生成函数签名（DI，便于测试）
type SummarizeFn func(ctx context.Context, messages []AgentMessage, maxTokens int, previousSummary string) (string, error)

func NewCompactor(settings CompactionSettings, contextLength int, summarizeFn SummarizeFn) *Compactor {
    return &Compactor{
        settings:        settings,
        contextLength:   contextLength,
        thresholdTokens: int(float64(contextLength) * settings.ThresholdPercent),
        summarizeFn:     summarizeFn,
    }
}

func (c *Compactor) Name() string { return "compactor" }

func (c *Compactor) ShouldCompact() bool {
    if !c.settings.Enabled { return false }
    if c.consecutiveFailures >= c.settings.MaxConsecutiveFailures { return false } // 熔断
    return c.lastPromptTokens > c.thresholdTokens
}

func (c *Compactor) UpdateFromResponse(usage llmprovider.Usage) {
    c.lastPromptTokens = usage.InputTokens
    c.lastCompTokens = usage.OutputTokens
}
```

### 13.4 Token 估算器

```go
// agentcore/context/estimator.go

// EstimateContextTokens 双轨 token 估算（借鉴 Pi）
// 优先使用最近 assistant 的 usage，补充后续消息的启发式估算
func EstimateContextTokens(messages []AgentMessage) ContextEstimate {
    usageInfo := getLastAssistantUsage(messages)
    if usageInfo == nil {
        estimated := 0
        for _, msg := range messages {
            estimated += estimateMessageTokens(msg)
        }
        return ContextEstimate{Total: estimated, LastUsageIndex: -1}
    }

    trailing := 0
    for i := usageInfo.index + 1; i < len(messages); i++ {
        trailing += estimateMessageTokens(messages[i])
    }
    total := usageInfo.tokens + trailing
    return ContextEstimate{
        Total: total, UsageTokens: usageInfo.tokens,
        TrailingTokens: trailing, LastUsageIndex: usageInfo.index,
    }
}

// estimateMessageTokens 单条消息的字符启发式估算（借鉴 Pi/Hermes）
// 文本: len/4, 图片: 1600 tokens（借鉴 Hermes _IMAGE_TOKEN_ESTIMATE）
func estimateMessageTokens(msg AgentMessage) int {
    chars := 0
    for _, part := range msg.Content {
        switch p := part.(type) {
        case TextContent:
            chars += len(p.Text)
        case ImageContent:
            chars += 6400 // 1600 * 4 (chars/token)
        case ToolCallContent:
            chars += len(p.Name) + len(p.ArgumentsJSON)
        case ToolResultContent:
            chars += len(p.Text)
        }
    }
    return (chars + 3) / 4
}
```

### 13.5 多层压缩 Pipeline

```go
// agentcore/context/pipeline.go

// CompactionPipeline 多层压缩管道（借鉴 Claude Code 五层设计，Go 简化为三层）
type CompactionPipeline struct {
    stages []CompactionStage
}

// CompactionStage 单个压缩阶段
type CompactionStage interface {
    Name() string
    // Process 处理消息列表，返回处理后的消息列表
    // changed=true 表示本阶段有实际修改
    Process(ctx context.Context, messages []AgentMessage) (result []AgentMessage, changed bool, err error)
}

// NewDefaultPipeline 创建默认三层 pipeline
func NewDefaultPipeline(engine ContextEngine) *CompactionPipeline {
    return &CompactionPipeline{
        stages: []CompactionStage{
            &ToolOutputPruner{},  // 第 1 层：清理旧 tool output（借鉴 Hermes）
            &LargeMessageSnip{},  // 第 2 层：截断超大单条消息（借鉴 Claude Code snip）
            &SummaryCompactor{    // 第 3 层：LLM 摘要压缩（借鉴 Pi + Hermes）
                engine: engine,
            },
        },
    }
}

// Run 依次执行所有阶段
func (p *CompactionPipeline) Run(ctx context.Context, messages []AgentMessage) ([]AgentMessage, error) {
    current := messages
    for _, stage := range p.stages {
        result, changed, err := stage.Process(ctx, current)
        if err != nil {
            return current, fmt.Errorf("compaction stage %s failed: %w", stage.Name(), err)
        }
        if changed {
            current = result
        }
    }
    return current, nil
}

// ToolOutputPruner 第 1 层：清理旧 tool output（借鉴 Hermes）
type ToolOutputPruner struct {
    MaxAgeFromTail int // 距尾部超过此消息数的 tool output 被清理
}

func (t *ToolOutputPruner) Name() string { return "tool_output_pruner" }

func (t *ToolOutputPruner) Process(ctx context.Context, messages []AgentMessage) ([]AgentMessage, bool, error) {
    // 保留尾部 N 条消息的 tool output，其余替换为占位符
    // "[Old tool output cleared to save context space]"
    // 同时截断 tool_call arguments 中超长字符串值（保持 JSON 合法性）
    // ...实现略
    return messages, false, nil
}
```

### 13.6 与 AgentLoopConfig 集成

```go
// 在现有 AgentLoopConfig 中新增字段

type AgentLoopConfig struct {
    // ... 现有字段 ...
    StreamFn       StreamFn
    ConvertToLlm   func([]AgentMessage) []llmprovider.Message
    BeforeToolCall func(BeforeToolCallContext) BeforeToolCallResult
    AfterToolCall  func(AfterToolCallContext) AfterToolCallResult

    // 新增：上下文管理
    ContextEngine  context.ContextEngine // 可选，nil 则不启用压缩
    CompactFn      context.CompactFn     // 可选，覆盖 ContextEngine 默认行为（DI 测试用）
    CompactionSettings context.CompactionSettings
}

// loop.go 中 turn 结束后的集成逻辑（伪代码）
//
// 在每次 tool 执行完毕、准备下一轮之前：
//   1. engine.UpdateFromResponse(lastUsage)
//   2. if engine.ShouldCompact():
//        emit AgentEvent{Type: "compaction_start"}
//        result, err := pipeline.Run(ctx, messages)
//        if err == nil:
//            messages = result
//            session.AppendEntry(CompactionEntry{...})
//            emit AgentEvent{Type: "compaction_end", Summary: result.Summary}
//        else:
//            consecutiveFailures++  // 熔断计数
```

---

## 十四、agentcore 会话管理子模块设计

### 14.1 核心类型

```go
// agentcore/session/types.go

// SessionEntry 会话条目（借鉴 Pi SessionTreeEntry 的 tree-based 模型）
type SessionEntry struct {
    ID        string    `json:"id"`
    ParentID  string    `json:"parent_id,omitempty"` // 空字符串表示 root
    Timestamp time.Time `json:"timestamp"`
    Type      string    `json:"type"` // discriminator
    Payload   any       `json:"-"`    // 具体类型根据 Type 反序列化
}

// --- Entry Payload 类型（借鉴 Pi 的 10 种 entry type，Go 简化为 7 种）---

// MessageEntry 消息条目
type MessageEntry struct {
    Message AgentMessage `json:"message"`
}

// CompactionEntry 压缩条目
type CompactionEntry struct {
    Summary          string `json:"summary"`
    FirstKeptEntryID string `json:"first_kept_entry_id"`
    TokensBefore     int    `json:"tokens_before"`
    FromHook         bool   `json:"from_hook,omitempty"`
}

// ModelChangeEntry 模型切换条目
type ModelChangeEntry struct {
    Provider string `json:"provider"`
    ModelID  string `json:"model_id"`
}

// BranchSummaryEntry 分支摘要条目（用于 fork 场景，借鉴 Pi）
type BranchSummaryEntry struct {
    FromID  string `json:"from_id"`
    Summary string `json:"summary"`
}

// CustomEntry 自定义条目（可扩展）
type CustomEntry struct {
    CustomType string `json:"custom_type"`
    Data       any    `json:"data,omitempty"`
}

// LeafEntry 叶子指针条目（借鉴 Pi，记录当前活跃分支）
type LeafEntry struct {
    TargetID string `json:"target_id"` // 空字符串表示 root
}

// LabelEntry 标签条目
type LabelEntry struct {
    TargetID string `json:"target_id"`
    Label    string `json:"label"`
}

// SessionMetadata 会话元数据
type SessionMetadata struct {
    ID        string    `json:"id"`
    CreatedAt time.Time `json:"created_at"`
    CWD       string    `json:"cwd,omitempty"`  // 工作目录（借鉴 Pi JsonlSessionMetadata）
    ParentID  string    `json:"parent_id,omitempty"` // fork 来源
}

// SessionContext 从 entry 链重建的会话上下文（借鉴 Pi buildSessionContext）
type SessionContext struct {
    Messages      []AgentMessage
    ThinkingLevel string
    Model         *ModelRef // nil 表示未切换过
}

// ModelRef 模型引用
type ModelRef struct {
    Provider string
    ModelID  string
}
```

### 14.2 SessionStorage 接口

```go
// agentcore/session/storage.go

// SessionStorage 会话存储接口（借鉴 Pi SessionStorage）
type SessionStorage interface {
    // GetMetadata 返回会话元数据
    GetMetadata() (*SessionMetadata, error)

    // GetLeafID 返回当前活跃分支的叶子 entry ID（空字符串表示空会话）
    GetLeafID() (string, error)

    // SetLeafID 设置当前活跃叶子（用于分支切换，借鉴 Pi moveTo）
    SetLeafID(leafID string) error

    // CreateEntryID 生成新 entry ID（UUIDv7 保证时间有序）
    CreateEntryID() string

    // AppendEntry 追加条目（自动设置 parentID 为当前 leafID）
    AppendEntry(entry SessionEntry) error

    // GetEntry 按 ID 查询单条
    GetEntry(id string) (*SessionEntry, error)

    // GetPathToRoot 从指定 leaf 沿 parentID 回溯到 root（借鉴 Pi）
    GetPathToRoot(leafID string) ([]SessionEntry, error)

    // GetEntries 返回全部条目（调试/导出用）
    GetEntries() ([]SessionEntry, error)

    // FindEntries 按类型过滤
    FindEntries(entryType string) ([]SessionEntry, error)
}
```

### 14.3 InMemoryStorage 实现

```go
// agentcore/session/memory.go

// InMemoryStorage 内存实现（测试和短期会话用，借鉴 Pi InMemorySessionStorage）
type InMemoryStorage struct {
    mu       sync.RWMutex
    metadata SessionMetadata
    entries  []SessionEntry
    byID     map[string]*SessionEntry
    leafID   string
}

func NewInMemoryStorage(metadata SessionMetadata) *InMemoryStorage {
    return &InMemoryStorage{
        metadata: metadata,
        entries:  make([]SessionEntry, 0, 64),
        byID:    make(map[string]*SessionEntry),
    }
}

func (s *InMemoryStorage) GetLeafID() (string, error) {
    s.mu.RLock()
    defer s.mu.RUnlock()
    return s.leafID, nil
}

func (s *InMemoryStorage) AppendEntry(entry SessionEntry) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    if entry.ParentID == "" {
        entry.ParentID = s.leafID
    }
    s.entries = append(s.entries, entry)
    s.byID[entry.ID] = &s.entries[len(s.entries)-1]
    // 自动推进 leaf（message/compaction 等实体 entry）
    if entry.Type != "leaf" && entry.Type != "label" {
        s.leafID = entry.ID
    }
    return nil
}

func (s *InMemoryStorage) GetPathToRoot(leafID string) ([]SessionEntry, error) {
    s.mu.RLock()
    defer s.mu.RUnlock()
    if leafID == "" { return nil, nil }

    var path []SessionEntry
    current, ok := s.byID[leafID]
    if !ok { return nil, fmt.Errorf("entry %s not found", leafID) }

    for current != nil {
        path = append([]SessionEntry{*current}, path...) // prepend
        if current.ParentID == "" { break }
        current, ok = s.byID[current.ParentID]
        if !ok { return nil, fmt.Errorf("parent entry %s not found", current.ParentID) }
    }
    return path, nil
}
```

### 14.4 JSONL Storage 实现

```go
// agentcore/session/jsonl.go

// JsonlStorage JSONL 文件存储实现（借鉴 Pi JsonlSessionStorage）
// 每条 entry 追加写入一行 JSON，读取时逐行解析
type JsonlStorage struct {
    mu       sync.Mutex
    filePath string
    metadata SessionMetadata
    entries  []SessionEntry        // 缓存的 entries
    byID     map[string]*SessionEntry
    leafID   string
    file     *os.File // append-only 文件句柄
}

// OpenJsonlStorage 打开已有的 JSONL 文件
func OpenJsonlStorage(filePath string) (*JsonlStorage, error) {
    // 逐行读取 JSONL，解析 entry，重建 byID map 和 leafID
    // ...
    return nil, nil
}

// CreateJsonlStorage 创建新的 JSONL 文件
func CreateJsonlStorage(filePath string, metadata SessionMetadata) (*JsonlStorage, error) {
    dir := filepath.Dir(filePath)
    if err := os.MkdirAll(dir, 0755); err != nil {
        return nil, fmt.Errorf("create session dir: %w", err)
    }
    f, err := os.OpenFile(filePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
    if err != nil {
        return nil, fmt.Errorf("create session file: %w", err)
    }

    s := &JsonlStorage{
        filePath: filePath,
        metadata: metadata,
        entries:  make([]SessionEntry, 0, 64),
        byID:    make(map[string]*SessionEntry),
        file:    f,
    }
    // 写入元数据头行
    header, _ := json.Marshal(metadata)
    fmt.Fprintf(f, "%s\n", header)
    return s, nil
}

func (s *JsonlStorage) AppendEntry(entry SessionEntry) error {
    s.mu.Lock()
    defer s.mu.Unlock()

    if entry.ParentID == "" {
        entry.ParentID = s.leafID
    }

    line, err := json.Marshal(entry)
    if err != nil {
        return fmt.Errorf("marshal entry: %w", err)
    }
    if _, err := fmt.Fprintf(s.file, "%s\n", line); err != nil {
        return fmt.Errorf("write entry: %w", err)
    }

    s.entries = append(s.entries, entry)
    s.byID[entry.ID] = &s.entries[len(s.entries)-1]
    if entry.Type != "leaf" && entry.Type != "label" {
        s.leafID = entry.ID
    }
    return nil
}
```

### 14.5 SessionRepo 接口与实现

```go
// agentcore/session/repo.go

// SessionRepo 会话仓库接口（借鉴 Pi SessionRepo）
type SessionRepo interface {
    Create(opts CreateOptions) (*Session, error)
    Open(metadata SessionMetadata) (*Session, error)
    List(opts ListOptions) ([]SessionMetadata, error)
    Delete(metadata SessionMetadata) error
    Fork(source SessionMetadata, opts ForkOptions) (*Session, error)
}

type CreateOptions struct {
    ID  string // 可选，空则自动生成 UUIDv7
    CWD string
}

type ListOptions struct {
    CWD string // 可选，按工作目录过滤
}

type ForkOptions struct {
    EntryID  string // fork 切点
    Position string // "before" | "at"（借鉴 Pi）
    CWD      string
}

// Session 封装 SessionStorage，提供高层操作（借鉴 Pi Session class）
type Session struct {
    storage SessionStorage
}

func NewSession(storage SessionStorage) *Session {
    return &Session{storage: storage}
}

// BuildContext 从当前 leaf 重建会话上下文（借鉴 Pi buildSessionContext）
func (s *Session) BuildContext() (*SessionContext, error) {
    leafID, err := s.storage.GetLeafID()
    if err != nil { return nil, err }

    entries, err := s.storage.GetPathToRoot(leafID)
    if err != nil { return nil, err }

    return buildSessionContext(entries)
}

// buildSessionContext 从 entry 链重建上下文
// 遇到 CompactionEntry 时跳过被压缩段，从 firstKeptEntryID 开始保留
func buildSessionContext(entries []SessionEntry) (*SessionContext, error) {
    ctx := &SessionContext{ThinkingLevel: "off"}

    var compaction *CompactionEntry
    for _, entry := range entries {
        switch entry.Type {
        case "compaction":
            if ce, ok := entry.Payload.(*CompactionEntry); ok {
                compaction = ce
            }
        case "model_change":
            if mc, ok := entry.Payload.(*ModelChangeEntry); ok {
                ctx.Model = &ModelRef{Provider: mc.Provider, ModelID: mc.ModelID}
            }
        }
    }

    // 收集消息：如果有 compaction，从 firstKeptEntryID 开始
    collecting := compaction == nil
    for _, entry := range entries {
        if !collecting && entry.ID == compaction.FirstKeptEntryID {
            collecting = true
        }
        if entry.Type == "compaction" {
            // 插入摘要消息
            ctx.Messages = append(ctx.Messages, AgentMessage{
                Role:    "compaction_summary",
                Content: []ContentPart{{Type: "text", Text: compaction.Summary}},
            })
            collecting = true
            continue
        }
        if collecting && entry.Type == "message" {
            if me, ok := entry.Payload.(*MessageEntry); ok {
                ctx.Messages = append(ctx.Messages, me.Message)
            }
        }
    }
    return ctx, nil
}

// AppendMessage 追加消息条目
func (s *Session) AppendMessage(msg AgentMessage) (string, error) {
    id := s.storage.CreateEntryID()
    return id, s.storage.AppendEntry(SessionEntry{
        ID:        id,
        Timestamp: time.Now(),
        Type:      "message",
        Payload:   &MessageEntry{Message: msg},
    })
}

// AppendCompaction 追加压缩条目
func (s *Session) AppendCompaction(result *context.CompactionResult) (string, error) {
    id := s.storage.CreateEntryID()
    return id, s.storage.AppendEntry(SessionEntry{
        ID:        id,
        Timestamp: time.Now(),
        Type:      "compaction",
        Payload: &CompactionEntry{
            Summary:          result.Summary,
            FirstKeptEntryID: result.FirstKeptEntryID,
            TokensBefore:     result.TokensBefore,
        },
    })
}

// AppendModelChange 追加模型切换条目
func (s *Session) AppendModelChange(provider, modelID string) (string, error) {
    id := s.storage.CreateEntryID()
    return id, s.storage.AppendEntry(SessionEntry{
        ID:        id,
        Timestamp: time.Now(),
        Type:      "model_change",
        Payload:   &ModelChangeEntry{Provider: provider, ModelID: modelID},
    })
}

// Fork 分叉会话（借鉴 Pi Session.moveTo + SessionRepo.fork）
func (s *Session) Fork(storage SessionStorage, entryID string) (*Session, error) {
    entries, err := s.storage.GetPathToRoot(entryID)
    if err != nil { return nil, err }

    for _, entry := range entries {
        if err := storage.AppendEntry(entry); err != nil {
            return nil, fmt.Errorf("fork append: %w", err)
        }
    }
    return NewSession(storage), nil
}
```

### 14.6 与 loop.go 集成

```go
// loop.go 中会话管理集成点（伪代码）

type AgentLoopConfig struct {
    // ... 现有字段 ...

    // 新增：会话管理
    Session *session.Session // 可选，nil 则不持久化
}

// runLoop 中的集成：
//
// 每次 assistant response 完成后：
//   if config.Session != nil {
//       config.Session.AppendMessage(assistantMsg)
//   }
//
// 每次 tool 执行完成后：
//   if config.Session != nil {
//       config.Session.AppendMessage(toolResultMsg)
//   }
//
// 压缩完成后：
//   if config.Session != nil {
//       config.Session.AppendCompaction(compactionResult)
//   }
//
// 恢复会话：
//   ctx, err := config.Session.BuildContext()
//   messages = ctx.Messages
```

---

## 十五、实施节奏

| 阶段 | 内容 | 依赖 |
|------|------|------|
| **P0** | `session/types.go` + `session/storage.go` + `session/memory.go` | 无 |
| **P1** | `context/types.go` + `context/engine.go` + `context/estimator.go` | P0 |
| **P2** | `context/compactor.go`（默认引擎，需 llmprovider.StreamFn） | P1 + llmprovider |
| **P3** | `session/jsonl.go` + `session/repo.go` | P0 |
| **P4** | `context/pipeline.go`（三层 pipeline） | P2 |
| **P5** | loop.go 集成 context + session | P2 + P3 |
| **P6** | 单元测试（InMemoryStorage + Mock ContextEngine） | P5 |

---

## 十六、llmprovider 深度问题分析与设计修正

> 来源：Pi `packages/ai/src/types.ts` + `api-registry.ts` + `register-builtins.ts` + `simple-options.ts` + `providers/openai-completions.ts`  
> 本章为对照 Pi 源码进行深度审查后发现的 **6 个关键结构性问题 + 6 个设计缺口**，全部修正方案在原有章节基础上追加；原有章节标注"已被修正"处请以本章为准。

---

### 16.1 关键结构性问题一：Registry 键设计错误

**问题**：当前 `Registry` 以 `providerName`（`"openai"`, `"qwen"`）为 key。  
**根因**：Pi 的注册表以 **api-protocol**（`"openai-completions"`, `"anthropic-messages"`, `"openai-responses"`）为 key。这意味着同一公司的不同协议（如 OpenAI 的 completions API vs responses API）是两个独立注册项，而非同一 provider 的两种方法。

```
Pi 注册表 key 示例：
  "openai-completions"   → streamOpenAICompletions / streamSimpleOpenAICompletions
  "openai-responses"     → streamOpenAIResponses   / streamSimpleOpenAIResponses
  "anthropic-messages"   → streamAnthropic         / streamSimpleAnthropic
  "google-generative-ai" → streamGoogle            / streamSimpleGoogle
  "bedrock-converse-stream" → streamBedrock        / ...

Provider（公司名）只是模型对象的一个元数据字段，不用于路由。
```

**影响**：
- 当前设计无法让 DeepSeek、Groq 复用同一个 `"openai-completions"` 实现 — 每个都要注册独立 provider
- 无法在不同模型上使用同一公司的不同 API 协议

**修正方案**（`registry.go` + `provider.go`）：

```go
// ApiType — api 协议类型（路由键）
// 对应 Pi 的 KnownApi
type ApiType string

const (
    ApiOpenAICompletions    ApiType = "openai-completions"
    ApiOpenAIResponses      ApiType = "openai-responses"
    ApiAnthropicMessages    ApiType = "anthropic-messages"
    ApiGoogleGenerativeAI   ApiType = "google-generative-ai"
    ApiGoogleVertex         ApiType = "google-vertex"
    ApiBedrockConverse      ApiType = "bedrock-converse-stream"
    ApiDashScopeMessages    ApiType = "dashscope-messages"     // Qwen 原生
    ApiMistralConversations ApiType = "mistral-conversations"
    ApiAzureOpenAIResponses ApiType = "azure-openai-responses"
)

// ApiProvider — 注册表的注册单元（对应 Pi ApiProvider<TApi, TOptions>）
// 以 ApiType 为 key，与公司/品牌名解耦
type ApiProvider interface {
    ApiType()    ApiType
    Stream(ctx context.Context, model Model, context Context, opts StreamOptions) (<-chan StreamEvent, error)
    StreamSimple(ctx context.Context, model Model, context Context, opts SimpleStreamOptions) <-chan StreamEvent
}

// Registry 修正：以 ApiType 为主键
type Registry struct {
    mu        sync.RWMutex
    providers map[ApiType]ApiProvider  // ← key 改为 ApiType
    bySource  map[string][]ApiType     // sourceId → []ApiType（用于插件卸载）
    modelCache sync.Map
}

func (r *Registry) Register(p ApiProvider, sourceId string)
func (r *Registry) Unregister(sourceId string)    // 清除 sourceId 下所有 ApiType
func (r *Registry) Get(api ApiType) (ApiProvider, bool)
func (r *Registry) Resolve(model Model) (ApiProvider, bool)  // 通过 model.Api 路由
```

> **与 §8.4 的关系**：§8.4 的 `Register(p Provider, profile *ProviderProfile, sourceId string)` 中 provider-name key 机制**以本节修正为准**，Registry 主键改为 `ApiType`，providerName 作为辅助查询索引。

---

### 16.2 关键结构性问题二：`Model` 对象缺少核心路由字段

**问题**：当前 `ModelCaps` 仅包含能力标记，缺少以下运行时必需字段：

| 缺失字段 | Pi 等价 | 用途 |
|---------|---------|------|
| `Api ApiType` | `Model.api: TApi` | 路由到正确的 ApiProvider |
| `BaseURL string` | `Model.baseUrl: string` | per-model endpoint（Azure 每个部署独立 URL）|
| `Compat any` | `Model.compat?: OpenAICompletionsCompat \| ...` | per-model 兼容性覆盖 |
| `Headers map[string]string` | `Model.headers?: Record<string,string>` | per-model 自定义请求头 |

**影响**：
- Azure OpenAI 每个部署有独立 endpoint，无法用单一 provider BaseURL 表达
- 不同 openai-compat 模型（DeepSeek vs Groq vs Qwen）有不同行为差异，必须 per-model 表达
- `StreamFn` 接受 `model string` 丢失所有上述信息，provider 实现内部无法感知

**修正方案**（`models.go` 升级）：

```go
// Model — 运行时模型对象（对应 Pi Model<TApi>）
// 同时承载路由信息和元数据，传递给 ApiProvider.Stream()
type Model struct {
    // ── 路由字段 ──
    ID      string  `json:"id"`       // 模型 ID，如 "gpt-4o"
    Api     ApiType `json:"api"`      // 协议类型，用于 Registry 路由
    Provider string `json:"provider"` // 公司名（仅元数据，不用于路由）

    // ── 请求时必需 ──
    BaseURL string            `json:"base_url"`           // provider endpoint，可 per-model 覆盖
    Headers map[string]string `json:"headers,omitempty"`  // per-model 自定义请求头

    // ── 兼容性覆盖（ApiType 特定，按需类型断言）──
    // 对 ApiOpenAICompletions: *OpenAICompletionsCompat
    // 对 ApiAnthropicMessages: *AnthropicMessagesCompat
    // 对 ApiGoogleGenerativeAI: *GoogleCompat（扩展）
    Compat any `json:"compat,omitempty"`

    // ── 能力元数据（原 ModelCaps 字段保留）──
    Name             string     `json:"name"`
    Modalities       []Modality `json:"modalities"`
    ContextWindow    int        `json:"context_window"`
    MaxOutput        int        `json:"max_output"`
    InputCostPer1K   float64    `json:"input_cost_per_1k"`
    OutputCostPer1K  float64    `json:"output_cost_per_1k"`
    SupportsTools    bool       `json:"supports_tools"`
    Reasoning        bool       `json:"reasoning"`

    // ── Thinking 能力（借鉴 Pi Model.thinkingLevelMap）──
    // 将各抽象 ThinkingLevel 映射到 provider 特定值（nil = 不支持该级别）
    ThinkingLevelMap ThinkingLevelMap `json:"thinking_level_map,omitempty"`
}

// ThinkingLevelMap: 各 ThinkingLevel → provider 特定字符串值（nil 表示不支持）
// 示例（Anthropic claude-3-7-sonnet）:
//   "low": "1024", "medium": "8192", "high": "16384", "xhigh": nil（不支持）
type ThinkingLevelMap map[ModelThinkingLevel]*string

// StreamFn 签名修正：接受 Model 对象而非 string（对应 Pi StreamFn）
type StreamFn func(
    ctx     context.Context,
    model   Model,               // ← 改为完整 Model 对象
    context Context,             // ← 见 §16.3，分离会话状态
    opts    SimpleStreamOptions,
) <-chan StreamEvent
```

> **与原有设计的关系**：原 `§5 StreamFn` 定义中 `model string` **以本节为准改为 `Model`**；原 `§2.5 ModelCaps` 演化为本节 `Model` 结构体，字段是原 ModelCaps 的超集。

---

### 16.3 关键结构性问题三：`Context` / `StreamOptions` 职责混淆

**问题**：当前 `StreamOptions` 包含 `SystemPrompt string` 和 `Tools []Tool`，与 `temperature`、`apiKey` 等请求参数混在一起。

**Pi 的分层**（严格分离，职责清晰）：

```typescript
// 会话状态（每次 LLM 调用携带，表示"要问什么"）
interface Context {
    systemPrompt?: string;
    messages: Message[];
    tools?: Tool[];
}

// 请求选项（表示"怎么问"，与会话内容无关）
interface StreamOptions {
    temperature?: number;
    maxTokens?: number;
    signal?: AbortSignal;
    apiKey?: string;
    cacheRetention?: CacheRetention;
    sessionId?: string;
    onPayload?: (payload, model) => payload | undefined;
    onResponse?: (response, model) => void;
    headers?: Record<string, string>;
    timeoutMs?: number;
    maxRetries?: number;
    maxRetryDelayMs?: number;
    metadata?: Record<string, unknown>;
}

// 高层选项（StreamOptions + reasoning，用于 streamSimple）
interface SimpleStreamOptions extends StreamOptions {
    reasoning?: ThinkingLevel;
    thinkingBudgets?: ThinkingBudgets; // per-level 自定义 token 预算
}
```

**修正方案**（`types.go` 重构）：

```go
// Context — 会话状态（对应 Pi Context）
// 传给 ApiProvider.Stream() 的会话内容，与请求参数解耦
type Context struct {
    SystemPrompt string    `json:"system_prompt,omitempty"`
    Messages     []Message `json:"messages"`
    Tools        []Tool    `json:"tools,omitempty"`
}

// StreamOptions — 纯请求参数（移除 SystemPrompt/Tools）
type StreamOptions struct {
    APIKey    string

    // ── 请求生命周期钩子 ──
    OnPayload  func(payload []byte, model Model) []byte
    OnResponse func(statusCode int, headers http.Header, model Model)

    // ── 请求级元数据 ──
    SessionId string
    Metadata  map[string]any

    // ── 超时与重试 ──
    TimeoutMs       int
    MaxRetries      int
    MaxRetryDelayMs int

    // ── Cache 偏好（新增，见 §16.6）──
    CacheRetention CacheRetention  // "none" | "short" | "long"

    // ── 传输（新增，见 §16.6）──
    Transport Transport  // "sse" | "websocket" | "auto"

    // ── 额外参数 ──
    Headers   map[string]string
    ExtraBody map[string]any
}

// SimpleStreamOptions — 高层选项（StreamOptions + Thinking）
// 对应 Pi SimpleStreamOptions；用于 StreamSimple 入口
type SimpleStreamOptions struct {
    StreamOptions
    Reasoning      ThinkingLevel   // "off"|"minimal"|"low"|"medium"|"high"|"xhigh"
    ThinkingBudgets ThinkingBudgets // per-level 自定义 token 预算（可选）
}

// ThinkingBudgets — 各 thinking level 的 token 预算（对应 Pi ThinkingBudgets）
type ThinkingBudgets struct {
    Minimal int  // 默认 1024
    Low     int  // 默认 2048
    Medium  int  // 默认 8192
    High    int  // 默认 16384
}
```

---

### 16.4 关键结构性问题四：缺少 `StreamSimple` 双入口与 Thinking 集中翻译

**问题**：当前只有 `Stream()` 一个入口，Thinking Level → budget 翻译逻辑散落在各 provider 内，造成重复且难以统一。

**Pi 方案**：
- `ApiProvider.stream()` — 低层，接受 provider-specific options，provider 完全掌控
- `ApiProvider.streamSimple()` — 高层，接受 `SimpleStreamOptions`，**中间层统一完成** `reasoning ThinkingLevel → {maxTokens, thinkingBudget}` 翻译后再调用内部 stream

**修正方案**（`provider.go` + 新增 `stream_options.go`）：

```go
// ApiProvider 接口（对应 Pi ApiProvider<TApi, TOptions>）
type ApiProvider interface {
    ApiType() ApiType

    // Stream — 低层入口，provider-specific options，完全控制
    Stream(ctx context.Context, model Model, conv Context, opts StreamOptions) <-chan StreamEvent

    // StreamSimple — 高层入口，thinking level 由调用方传入，provider 内部翻译
    // 合约：永不 panic；错误编码进 channel
    StreamSimple(ctx context.Context, model Model, conv Context, opts SimpleStreamOptions) <-chan StreamEvent
}

// stream_options.go — Thinking 集中翻译（对应 Pi simple-options.ts）
// 各 provider 的 StreamSimple 实现调用此函数

// AdjustMaxTokensForThinking 计算 thinking 场景下的 maxTokens 和 thinkingBudget
// 对应 Pi adjustMaxTokensForThinking()
func AdjustMaxTokensForThinking(
    baseMaxTokens int,     // 0 = 未显式指定，用模型上限
    modelMaxTokens int,
    level ThinkingLevel,
    budgets *ThinkingBudgets, // nil = 使用默认值
) (maxTokens int, thinkingBudget int) {
    defaults := ThinkingBudgets{Minimal: 1024, Low: 2048, Medium: 8192, High: 16384}
    b := mergeBudgets(defaults, budgets)

    // "xhigh" 降级到 "high"（Pi clampReasoning）
    if level == ThinkingLevelXHigh {
        level = ThinkingLevelHigh
    }

    budget := b.forLevel(level)
    if baseMaxTokens == 0 {
        return modelMaxTokens, budget
    }
    max := min(baseMaxTokens+budget, modelMaxTokens)
    if max <= budget {
        budget = max(0, max-1024) // 保留至少 1024 output tokens
    }
    return max, budget
}

// ClampThinkingLevel 将 "xhigh" 降级为 "high"（不支持 xhigh 的 provider 使用）
func ClampThinkingLevel(level ThinkingLevel) ThinkingLevel {
    if level == ThinkingLevelXHigh { return ThinkingLevelHigh }
    return level
}
```

---

### 16.5 关键结构性问题五：`openaicompat/` Compat 字段覆盖不足

**问题**：当前 `openaicompat/` 只有 `QwenVideoMode bool`，无法表达不同 OpenAI-compat provider 的行为差异。

**Pi 的 `OpenAICompletionsCompat`** 有 18 个字段，Go 侧提取最关键的 12 个：

```go
// OpenAICompletionsCompat — per-model OpenAI completions 协议兼容性覆盖
// 放在 Model.Compat 字段中（ApiType == ApiOpenAICompletions 时）
// 对应 Pi types.ts OpenAICompletionsCompat
type OpenAICompletionsCompat struct {
    // ── 功能支持标志（nil = 自动从 baseUrl 检测）──
    SupportsStore             *bool // 支持 `store` 字段
    SupportsDeveloperRole     *bool // 支持 `developer` role（而非 `system`）
    SupportsReasoningEffort   *bool // 支持 `reasoning_effort`
    SupportsUsageInStreaming   *bool // 支持 `stream_options: {include_usage: true}`
    SupportsStrictMode        *bool // 支持 tool definition 中的 `strict` 字段

    // ── 消息格式强制要求 ──
    RequiresToolResultName             *bool // tool result 消息必须有 `name` 字段
    RequiresAssistantAfterToolResult   *bool // tool result 后必须有 assistant 消息
    RequiresThinkingAsText             *bool // thinking block 必须转为 <thinking>...</thinking> 文本块
    RequiresReasoningContentOnReplayed *bool // 重放的 assistant 消息必须包含空 reasoning_content

    // ── Thinking/Reasoning 格式 ──
    // 控制如何传递 reasoning 参数（不同 provider 格式不同）
    // "openai"           → reasoning_effort: "low"|"medium"|"high"
    // "deepseek"         → thinking: {type:"enabled_budget",...} + reasoning_effort
    // "qwen"             → enable_thinking: true（顶层字段）
    // "qwen-chat-template" → chat_template_kwargs: {enable_thinking: true}
    // "together"         → reasoning: {enabled: true} + reasoning_effort
    // "openrouter"       → reasoning: {effort: "low"|...}
    ThinkingFormat ThinkingFormat // "" = 默认 "openai"

    // ── MaxTokens 字段名 ──
    MaxTokensField string // "max_completion_tokens"（OpenAI o 系列）或 "max_tokens"（默认）

    // ── Cache Control ──
    // "anthropic" = 在 system prompt、最后一个 tool 定义、最后一条 user/assistant
    //   文本内容上添加 Anthropic 风格的 cache_control 标记
    CacheControlFormat string // "" | "anthropic"

    // ── Qwen 视频模式（原有，保留）──
    QwenVideoMode bool // true = 启用 Qwen video content type 扩展
}

// ThinkingFormat — reasoning 参数编码格式
type ThinkingFormat string

const (
    ThinkingFormatOpenAI          ThinkingFormat = "openai"
    ThinkingFormatDeepSeek        ThinkingFormat = "deepseek"
    ThinkingFormatQwen            ThinkingFormat = "qwen"
    ThinkingFormatQwenChatTpl     ThinkingFormat = "qwen-chat-template"
    ThinkingFormatTogether        ThinkingFormat = "together"
    ThinkingFormatOpenRouter      ThinkingFormat = "openrouter"
)

// AnthropicMessagesCompat — per-model Anthropic 协议兼容性覆盖
// 放在 Model.Compat 中（ApiType == ApiAnthropicMessages）
type AnthropicMessagesCompat struct {
    SupportsEagerToolInputStreaming *bool // 是否接受 per-tool eager_input_streaming
    SupportsLongCacheRetention     *bool // 是否支持 1h cache retention
    SendSessionAffinityHeaders     *bool // 是否发送 x-session-affinity 头
    SupportsCacheControlOnTools    *bool // tool 定义是否接受 cache_control
}
```

**已验证的 compat 配置**（`providers/openaicompat/profiles.go`）：

| Provider | API | ThinkingFormat | 特殊 Compat |
|----------|-----|----------------|------------|
| `deepseek` | `openai-completions` | `"deepseek"` | `RequiresThinkingAsText: true` |
| `groq` | `openai-completions` | `"openai"` | `SupportsStore: false` |
| `qwen-compat` | `openai-completions` | `"qwen"` | `QwenVideoMode: true` |
| `together` | `openai-completions` | `"together"` | — |
| `fireworks` | `openai-completions` | `"openai"` | `CacheControlFormat: "anthropic"` |
| `openrouter` | `openai-completions` | `"openrouter"` | `RequiresAssistantAfterToolResult: true` |

> **与 §8.5 的关系**：§8.5 中 `openaicompat/` 复用策略**以本节为准**；`OpenAICompatProvider` 中的 `qwenVideoMode bool` 扩展为完整 `OpenAICompletionsCompat` 结构体。

---

### 16.6 设计缺口补全

#### 16.6.1 ThinkingLevel 补全（对应 Pi `ThinkingLevel`）

```go
// ThinkingLevel — 推理强度级别（对应 Pi ThinkingLevel + ModelThinkingLevel）
type ThinkingLevel string

const (
    ThinkingLevelOff     ThinkingLevel = "off"      // 不启用推理
    ThinkingLevelMinimal ThinkingLevel = "minimal"  // ← 新增，最小推理（Pi 默认）
    ThinkingLevelLow     ThinkingLevel = "low"
    ThinkingLevelMedium  ThinkingLevel = "medium"
    ThinkingLevelHigh    ThinkingLevel = "high"
    ThinkingLevelXHigh   ThinkingLevel = "xhigh"    // ← 新增，仅特定模型支持
)

type ModelThinkingLevel = ThinkingLevel  // "off" | ThinkingLevel

// ThinkingLevelMap — per-model 级别映射（nil = 该级别不支持）
// 对应 Pi Model.thinkingLevelMap?: Partial<Record<ModelThinkingLevel, string | null>>
// 示例：Claude claude-3-7-sonnet 的 thinkingBudget token 值
//   "minimal": "1024", "low": "2048", "medium": "8192", "high": "16384", "xhigh": nil
type ThinkingLevelMap map[ThinkingLevel]*string
```

#### 16.6.2 CacheRetention（对应 Pi `CacheRetention`）

```go
// CacheRetention — prompt cache 保留时长偏好（对应 Pi CacheRetention）
// provider 按自己支持的机制映射此偏好
//   "none"  → 不启用 cache（即使 provider 默认启用也禁用）
//   "short" → 短期 cache（OpenAI ephemeral，Anthropic 5min TTL，默认）
//   "long"  → 长期 cache（OpenAI 24h，Anthropic 1h TTL）
type CacheRetention string

const (
    CacheRetentionNone  CacheRetention = "none"
    CacheRetentionShort CacheRetention = "short"
    CacheRetentionLong  CacheRetention = "long"
)
```

#### 16.6.3 Transport（对应 Pi `Transport`）

```go
// Transport — 传输方式偏好（对应 Pi Transport）
// 不支持该选项的 provider 忽略此字段
type Transport string

const (
    TransportSSE             Transport = "sse"
    TransportWebSocket       Transport = "websocket"
    TransportWebSocketCached Transport = "websocket-cached"
    TransportAuto            Transport = "auto"  // provider 自行选择（默认）
)
```

#### 16.6.4 StreamEvent / Usage 补充字段

```go
// StreamResult — 流结束后的聚合结果（升级版，对应 Pi AssistantMessage 元数据）
type StreamResult struct {
    Usage         Usage
    StopReason    StopReason
    // ── 新增：provider 返回的响应元数据 ──
    ResponseModel string    // 实际使用的模型（如 OpenRouter auto 路由后的具体模型）
    ResponseID    string    // provider 特定响应 ID（用于问题追踪）
    Diagnostics   []Diagnostic  // 脱敏的 provider 级诊断信息（故障和恢复）
    Err           error
}

// Diagnostic — provider 运行时诊断（对应 Pi AssistantMessageDiagnostic）
// 包含脱敏后的失败/重试/降级信息，供调试使用，不含敏感数据
type Diagnostic struct {
    Type    string `json:"type"`    // "retry", "fallback", "rate_limit", etc.
    Message string `json:"message"`
}

// StopReason — 流终止原因（对应 Pi StopReason）
type StopReason string

const (
    StopReasonStop     StopReason = "stop"
    StopReasonLength   StopReason = "length"
    StopReasonToolUse  StopReason = "tool_use"
    StopReasonError    StopReason = "error"
    StopReasonAborted  StopReason = "aborted"
)
```

#### 16.6.5 ToolCall 补充 `thoughtSignature`（对应 Pi `ToolCall.thoughtSignature`）

```go
type ToolCall struct {
    ID               string          `json:"id"`
    Name             string          `json:"name"`
    Arguments        json.RawMessage `json:"arguments"`
    // Google Thinking 上下文复用（对应 Pi ToolCall.thoughtSignature）
    // 不透明签名，回传给 Google API 以复用 thought context，降低延迟
    ThoughtSignature string          `json:"thought_signature,omitempty"`
}
```

#### 16.6.6 惰性加载错误传播（对应 Pi `createLazyStream`）

Pi 的 `register-builtins.ts::createLazyStream` 在 provider 模块动态加载失败时，不会 panic 也不会让调用方得到 Go 的 `nil channel`，而是立即返回一个 `channel`，随后把加载错误编码为 `StreamEvent{Type: StreamEventError}`。

```go
// providers/loader.go — 惰性加载错误传播（对应 Pi createLazyStream）
//
// 使用场景：provider 的 HTTP client 依赖外部 SDK（如 AWS SDK），加载时可能失败。
// 按需加载：首次调用 StreamSimple 时才初始化 HTTP client，而非 init() 时。

// LazyProvider 包装一个延迟初始化的 ApiProvider
type LazyProvider struct {
    apiType ApiType
    once    sync.Once
    inner   ApiProvider
    initErr error
    initFn  func() (ApiProvider, error)
}

func NewLazyProvider(api ApiType, initFn func() (ApiProvider, error)) *LazyProvider {
    return &LazyProvider{apiType: api, initFn: initFn}
}

func (l *LazyProvider) ApiType() ApiType { return l.apiType }

func (l *LazyProvider) StreamSimple(ctx context.Context, model Model, conv Context, opts SimpleStreamOptions) <-chan StreamEvent {
    l.once.Do(func() {
        l.inner, l.initErr = l.initFn()
    })
    if l.initErr != nil {
        // 加载失败：编码为 error 事件，不 panic（对应 Pi createLazyStream error path）
        ch := make(chan StreamEvent, 1)
        ch <- StreamEvent{Type: StreamEventError, Err: fmt.Errorf("provider init: %w", l.initErr)}
        close(ch)
        return ch
    }
    return l.inner.StreamSimple(ctx, model, conv, opts)
}
```

---

### 16.7 修正对实现优先级的影响

以下条目**需要修改**（相较于 §十 的原有实现顺序）：

| 阶段 | 原条目 | 修正内容 |
|------|--------|---------|
| **P0** | `types.go` 核心类型 | 拆分为 `Context` 结构 + 精简 `StreamOptions` + 新增 `SimpleStreamOptions` |
| **P0** | `provider.go` Provider 接口 | 改为 `ApiProvider` 接口，双入口 `Stream/StreamSimple`，key 改 `ApiType` |
| **P0** | `registry.go` Registry | 主键改为 `ApiType`，辅助 providerName 索引 |
| **P0** | `models.go` 模型元数据 | `ModelCaps` → `Model`（增加 `Api/BaseURL/Compat/Headers/ThinkingLevelMap`）|
| **P0** | `stream_options.go`（新增） | `AdjustMaxTokensForThinking` + `ClampThinkingLevel` 集中翻译函数 |
| **P0** | `providers/openai/` | 实现 `StreamSimple`，调用 `AdjustMaxTokensForThinking` |
| **P0** | `providers/qwen/` | 同上；Compat 参数改为 `OpenAICompletionsCompat` |
| **P1** | `providers/openaicompat/profiles.go` | 补充完整 `OpenAICompletionsCompat`（ThinkingFormat/RequiresToolResultName 等）|
| **P1** | `agentcore/stream_fn.go` | `StreamFn` 签名改为接受 `Model` 对象 |
| **P1** | `WrapStreamFn` | 适配新 `ApiProvider.StreamSimple` 签名 |

**不受影响的条目**（§十三~§十五 agentcore/context/session 设计维持不变）：
- `agentcore/hooks.go`、`loop.go`（`BeforeToolCall/AfterToolCall` 设计不变）
- `agentcore/context/` 全部（ContextEngine 设计不变）
- `agentcore/session/` 全部（SessionEntry/SessionRepo 设计不变）
