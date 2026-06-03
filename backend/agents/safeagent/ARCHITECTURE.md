# SafeAgent 技术架构深度分析

## 1. 整体架构分层

```
┌─────────────────────────────────────────────────────────────────┐
│                        入口层 (safeagent.go)                      │
│  New(cfg Config) → 组装编排器, 注入 Task 工具                      │
├─────────────────────────────────────────────────────────────────┤
│  提示词层 (prompts.go)     │  类型层 (types.go)                    │
│  OrchestratorPrompt       │  Config, SceneInput                  │
│  5 个子 Agent Prompt      │  DetectionResult, RiskAssessment     │
│  buildAgentDefinitions()  │  InspectionDecision, WorkOrder       │
│                           │  NotificationResult, InspectionCtx   │
├─────────────────────────────────────────────────────────────────┤
│  子Agent层 (subagents.go)  │  工具层 (tools.go)                   │
│  NewVisionAgent()         │  buildVisionTools() + VLM 调用        │
│  NewRiskAgent()           │  buildRiskTools()                    │
│  NewDecisionAgent()       │  buildDecisionTools()                │
│  NewWorkflowAgent()       │  buildWorkflowTools()                │
│  NewNotifyAgent()         │  buildNotifyTools()                  │
├─────────────────────────────────────────────────────────────────┤
│                    视频会话层 (video_session.go)                   │
│  VideoSession: 滑动窗口帧调度 + SSE 事件流                          │
├─────────────────────────────────────────────────────────────────┤
│  依赖: agentcore  │  agents/types  │  tools/agenttool  │  llmprovider │
└─────────────────────────────────────────────────────────────────┘
```

## 2. 5 阶段安全巡检管道

```
用户输入 (SceneInput)
    │
    ▼
┌──────────────────┐
│   Orchestrator   │  System Prompt: OrchestratorPrompt
│   (agentcore)    │  工具: Task (唯一)
└──────┬───────────┘
       │ 调用 Task(subagent_type="vision_agent", prompt=SceneInput)
       ▼
┌──────────────────┐     detect_objects ──→ VLM API (真实调用)
│  VisionAgent     │     analyze_scene_context
│  (context隔离)    │
└──────┬───────────┘
       │ 返回 DetectionResult JSON
       ▼
┌──────────────────┐     lookup_regulation
│  RiskAgent       │     evaluate_risk
│  (context隔离)    │
└──────┬───────────┘
       │ 返回 RiskAssessment JSON
       ▼
┌──────────────────┐     confirm_hazard
│  DecisionAgent   │     determine_strategy
│  (context隔离)    │     assign_person
└──────┬───────────┘
       │ 返回 InspectionDecision JSON
       ▼
┌──────────────────┐     create_order → dispatch_order
│  WorkflowAgent   │     → verify_completion → close_order
│  (context隔离)    │
└──────┬───────────┘
       │ 返回 WorkOrder JSON
       ▼
┌──────────────────┐     send_notification
│  NotifyAgent     │     generate_report
│  (context隔离)    │
└──────┬───────────┘
       │ 返回 NotificationResult JSON
       ▼
┌──────────────────┐
│ InspectionContext│  汇总所有阶段结果
│ (最终输出)        │
└──────────────────┘
```

### 管道关键约束

- **严格串行**: 每个阶段必须在下一阶段开始前完成
- **JSON 透传**: 每次 Task 调用的 prompt 是上一阶段输出的完整 JSON 字符串
- **失败即停**: 任何阶段失败立即停止，不继续后续阶段
- **上下文隔离**: 每个子 Agent 创建时 Messages 为空，等价于全新会话

## 3. New() 组装流程

```
New(cfg Config)
│
├─ 1. 参数默认值
│     OrchestratorMaxIter → 20 (默认)
│     SubAgentMaxIter → 10 (默认)
│
├─ 2. buildAgentDefinitions()
│     构建 5 个 AgentDefinition:
│     ├─ 视觉识别Agent (vision_agent)
│     ├─ 风险分析Agent (risk_agent)
│     ├─ 处置决策Agent (decision_agent)
│     ├─ 工单闭环Agent (workflow_agent)
│     └─ 通知上报Agent (notify_agent)
│     每个包含: Name, AgentType, WhenToUse, Description, Tools[]
│
├─ 3. buildSpawnSubAgent(subCfg)
│     返回 func(ctx, SubAgentParams) → (string, error)
│     内部 switch p.SubagentType:
│     ├─ "vision_agent"   → NewVisionAgent(cfg).Prompt(ctx, p.Prompt)
│     ├─ "risk_agent"     → NewRiskAgent(cfg).Prompt(ctx, p.Prompt)
│     ├─ "decision_agent" → NewDecisionAgent(cfg).Prompt(ctx, p.Prompt)
│     ├─ "workflow_agent" → NewWorkflowAgent(cfg).Prompt(ctx, p.Prompt)
│     └─ "notify_agent"   → NewNotifyAgent(cfg).Prompt(ctx, p.Prompt)
│     每个子Agent执行后收集 TextDelta 拼接为字符串返回
│
├─ 4. buildTaskTool(spawnFn, agentDefs)
│     ├─ 创建 agenttool.New() 实例 (WithAllowedSubagentTypes, WithAgentCatalog)
│     ├─ 构造 agentcore.AgentTool:
│     │   Name: "Task"
│     │   Description: "派发专项子Agent执行安全巡检管道中的单个阶段任务"
│     │   DynamicDescription: 渲染 agenttool 的 Prompt (AgentToolIsCoordinator=true)
│     │   Execute: 手动构造 agents.ToolUseContext{SpwanSubAgent: spawnFn}
│     │             → 调用 impl.Call(ctx, args, toolCtx)
│     └─ ★ 绕过 tools/adapter.go: 因为 agentcore.ToolExecContext 无 SpawnSubAgent 字段
│
└─ 5. 创建 orchestrator
      agentcore.NewAgent(loopCfg, OrchestratorPrompt)
      orchestrator.AddTool(taskTool)
      返回 orchestrator
```

## 4. buildSpawnSubAgent 子 Agent 调度机制

```
buildSpawnSubAgent(cfg) → func(ctx, SubAgentParams) (string, error)

调用流程:
  1. switch p.SubagentType 路由到对应构造函数
  2. 每个构造函数创建全新的 agentcore.Agent:
     - 空 Messages (上下文隔离)
     - 独立的 IterationBudget (默认 10 次迭代)
     - 仅注册领域工具 (无 Task 工具)
  3. sub.Prompt(ctx, p.Prompt) 启动子 Agent 循环
  4. 消费 AgentEvent channel:
     - AgentEventTextDelta → 拼接文本
     - AgentEventError → 返回错误
  5. 返回完整文本结果给 orchestrator
```

### 子 Agent 配置

| 子 Agent | 工具集 | 特殊依赖 |
|----------|--------|---------|
| VisionAgent | detect_objects, analyze_scene_context | VLM Model + APIKey |
| RiskAgent | lookup_regulation, evaluate_risk | 无 |
| DecisionAgent | confirm_hazard, determine_strategy, assign_person | 无 |
| WorkflowAgent | create_order, dispatch_order, verify_completion, close_order | 无 |
| NotifyAgent | send_notification, generate_report | 无 |

## 5. 工具设计哲学

大部分工具采用 **"最小脚手架"** 模式 —— 返回极简数据，让 LLM 自由推理：

| 工具 | 返回内容 | 设计意图 |
|------|---------|---------|
| `lookup_regulation` | `{"violation_type":"...","regulations":[]}` | LLM 自带法规知识，无需外部查询 |
| `evaluate_risk` | 直接 echo 输入 args | LLM 自行评估风险等级 |
| `determine_strategy` | 直接 echo 输入 args | LLM 自行决定策略 |
| `assign_person` | 直接 echo 输入 args | LLM 自行分配责任人 |
| `dispatch_order` | 直接 echo 输入 args | 透传即可 |
| `analyze_scene_context` | 包装为 `{detection, location, ready:true}` | LLM 自行分析上下文 |

**例外** — 有副作用的工具返回真实数据：

| 工具 | 返回内容 |
|------|---------|
| `detect_objects` | 真实 VLM 调用结果 (DetectionResult JSON) |
| `confirm_hazard` | `{"hazard_confirmed":true,"hazard_id":"H-{timestamp}"}` |
| `create_order` | `{"order_id":"WO-{timestamp}","status":"created",...}` |
| `verify_completion` | `{"verified":true,"all_tasks_completed":true,...}` |
| `close_order` | `{"status":"closed","closed_at":"...",...}` |
| `send_notification` | `{"sent":true,"sent_at":"...",...}` |
| `generate_report` | `{"report_url":"https://safeagent.local/reports/{ts}",...}` |

## 6. VLM 视觉检测流程 (detect_objects)

```
detect_objects 工具执行流程:

1. 解析参数 {image_url, description}
       │
2. fetchImageAsDataURL(imageURL)
       │
       ├─ http://localhost:8080/api/safeagent/video/frames/*
       │    → 直接从 os.TempDir()/safeagent-frames/ 读取本地文件
       │
       ├─ http:// 或 https://
       │    → http.Get() 远程下载
       │
       └─ 其他 scheme → 报错
       │
3. 构建 VLM 请求
   ContentParts: [ImageURL, Text]
   Text = "场景描述：{description}\n\n{vlmDetectionPrompt}"
       │
4. llmprovider.StreamSimple(ctx, vlmModel, conv, opts)
       │
5. 解析 VLM 响应
       │
       ├─ 尝试解析为 DetectionResult JSON
       │   ├─ 先剥离 ```json ... ``` 或 ``` ... ``` markdown fences
       │   └─ json.Unmarshal → 成功则直接返回
       │
       └─ 解析失败 → 包装为 DetectionResult{Summary: rawText, Confidence: 0.7}
```

### fetchImageAsDataURL 本地帧优化

视频帧 URL 格式: `http://localhost:8080/api/safeagent/video/frames/{filename}`

识别到此前缀后，直接从 `os.TempDir()/safeagent-frames/{filename}` 读取文件，避免本地网络往返。

## 7. VideoSession 实时视频巡检

### 滑动窗口策略

```
状态机:
                    ┌──────────┐
         Submit()   │  IDLE    │
         ─────────→ │          │
                    └─────┬────┘
                          │ startLocked(job)
                          ▼
                    ┌──────────┐     Submit()
                    │ RUNNING  │ ──────────────→ pending = new job
                    │ (1 job)  │ ←────────────── 旧 pending → FrameDrop 事件
                    └─────┬────┘
                          │ finishFrame()
                          ├─ 有 pending → startLocked(pending)
                          └─ 无 pending → IDLE
```

**核心策略**: 最多 1 running + 1 pending。新帧覆盖 pending 槽位，丢弃旧 pending 帧并发送 `VideoEventFrameDrop`。

### SSE 事件类型

| 事件类型 | 触发时机 | 关键字段 |
|---------|---------|---------|
| `frame_start` | 开始处理一帧 | frame_idx, timestamp, running_idx |
| `frame_drop` | pending 帧被覆盖 | frame_idx (被丢弃的帧) |
| `frame_done` | 帧处理完成 | frame_idx, report (InspectionContext JSON) |
| `queue_status` | 队列状态变化 | running_idx, pending_idx |
| `text_delta` | 编排器输出文本 | delta, frame_idx |
| `thinking` | 编排器思考过程 | delta, frame_idx |
| `tool_start` | 工具调用开始 | tool_name, tool_args |
| `tool_end` | 工具调用结束 | tool_name, tool_result |
| `error` | 发生错误 | err_msg |
| `session_end` | 会话结束 | - |

### runFrame 执行流程

```
runFrame(ctx, job)
│
├─ 1. 构建 SceneInput{Description, ImageURL}
│      → json.Marshal 为 prompt 字符串
│
├─ 2. orchestrator := New(s.cfg)
│      ch := orchestrator.Prompt(ctx, prompt)
│
├─ 3. 消费 AgentEvent channel
│      每个事件转发为 VideoEvent:
│      ├─ AgentEventTextDelta → VideoEventTextDelta
│      ├─ AgentEventThinkingDelta → VideoEventThinking
│      ├─ AgentEventToolStart → VideoEventToolStart
│      ├─ AgentEventToolEnd → VideoEventToolEnd
│      └─ AgentEventError → VideoEventError
│      同时检查 s.closeCh 是否已关闭 (Stop)
│
├─ 4. finishFrame(job, fullText, cancelled)
│      ├─ extractInspectionJSON(fullText) 提取 {"input":...} JSON
│      ├─ 发送 VideoEventFrameDone (含 report)
│      └─ 检查 pending 队列，启动下一帧
│
└─ 5. safeSend() 非阻塞发送到 eventCh (容量 256)
       消费者慢时丢弃事件 (非关键遥测数据)
```

### extractInspectionJSON 解析策略

从编排器完整文本输出中查找第一个 `{"input"` 起始的 JSON 对象，通过括号深度计数提取完整 JSON，再 `json.Unmarshal` 验证合法性。

## 8. 关键设计决策

### 8.1 Task 工具绕过 tools/adapter.go

**问题**: `agentcore.ToolExecContext` 没有 `SpawnSubAgent` 字段，而 `agents.ToolUseContext` 有。`tools/adapter.go` 的 `ToAgentTool` 无法填充此字段。

**方案**: `buildTaskTool` 直接构造 `agentcore.AgentTool`，在 Execute 闭包中手动创建 `agents.ToolUseContext{SpawnSubAgent: spawnFn}`，绕过 adapter 层。

```go
// safeagent.go:86-101
Execute: func(texCtx *agentcore.ToolExecContext) agentcore.AgentToolResult {
    toolCtx := &agents.ToolUseContext{
        Ctx:           texCtx.Ctx,
        SpawnSubAgent: spawnFn,  // ← 手动注入
    }
    result, err := impl.Call(texCtx.Ctx, texCtx.Args, toolCtx)
    ...
}
```

### 8.2 上下文隔离

每个子 Agent 通过 `agentcore.NewAgent()` 创建，Messages 初始为空。这等价于:
- hermes-agent 的 "fresh conversation per task"
- opencode 的 "session isolation per Task call"

**优势**: 子 Agent 不受编排器历史污染，每个阶段独立推理。

### 8.3 工具设计: 重 LLM 推理，轻工具实现

大部分工具仅做参数透传或返回最小脚手架，将推理负担交给 LLM。这与 claude-code 的 `Task` 工具设计一致 —— 工具是"能力声明"，实际逻辑由 LLM 驱动。

### 8.4 迭代预算分层

| 层级 | 默认预算 | 配置项 |
|------|---------|--------|
| Orchestrator | 20 次迭代 | `OrchestratorMaxIter` |
| SubAgent | 10 次迭代 | `SubAgentMaxIter` |

编排器需要更多迭代因为它要依次调用 5 个子 Agent（每个至少 1 次迭代）。

## 9. 类型体系

```
SceneInput {scene_description, image_url?, location?}
    │
    └──→ DetectionResult {objects[], violations[], confidence, summary}
            │
            └──→ RiskAssessment {risks[], overall_level, summary}
                    │   └─ RiskItem {code, description, level, regulation?}
                    │
                    └──→ InspectionDecision {action, assignee?, deadline?, steps[], rationale}
                            │
                            └──→ WorkOrder {id, status, assignee, tasks[], created_at, closed_at?}
                                    │
                                    └──→ NotificationResult {channels[], report_url?, summary}

最终汇总: InspectionContext {input, detection?, risk?, decision?, work_order?, notification?}
```

## 10. 集成测试覆盖

| 测试 | 范围 | 超时 |
|------|------|------|
| `TestSafeAgentE2E_FullPipeline` | 完整 5 阶段编排 | 8 分钟 |
| `TestSafeAgentE2E_VisionAgent` | VisionAgent 独立测试 | 2 分钟 |
| `TestSafeAgentE2E_RiskAgent` | RiskAgent 独立测试 | 2 分钟 |
| `TestSafeAgentE2E_DecisionAgent` | DecisionAgent 独立测试 | 2 分钟 |

测试通过 `//go:build integration` 标签隔离，需要 `.env` 配置 LLM 和 VLM 凭证。遇到 HTTP 4xx 错误时 `t.Skip` 而非 `t.Fail`（模型行为差异）。

## 11. 依赖关系

```
safeagent
├── agentcore          ← Agent, AgentLoopConfig, AgentTool, AgentEvent, RunAgentLoop
├── agents             ← AgentDefinition, SubAgentParams, ToolUseContext
├── tools/agenttool    ← agenttool.New() Task 工具核心实现
├── llmprovider        ← Model, StreamSimple, ContentPart, StreamOptions
└── internal/envconfig ← 测试中加载 .env 配置
```
