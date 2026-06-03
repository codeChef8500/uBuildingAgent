# SafeAgent 视频抽帧与架构优化方案

针对智慧工地视频文件上传场景，优化抽帧策略和 SafeAgent 管道效率，降低延迟和成本，同时为未来 RTSP 实时流接入预留扩展点。

## 1. 视频抽帧策略优化

### 当前问题
- 固定 5 秒间隔，无法自适应场景变化
- 滑动窗口 3 帧跨度 15 秒，时序关联弱
- 每帧都跑完整 5 阶段管道，延迟 10-25 秒/帧

### 优化方案：自适应分层采样

```
┌─────────────────────────────────────────────────────┐
│  Layer 0: 帧间差分预判（纯图像处理，无 LLM 调用）      │
│  - 对相邻帧做像素级差分，计算变化区域占比               │
│  - 变化 < 阈值 → 跳过（场景静止，无需重复分析）          │
│  - 变化 > 阈值 → 进入 Layer 1                         │
├─────────────────────────────────────────────────────┤
│  Layer 1: VLM 视觉检测（仅关键帧触发）                  │
│  - 采样间隔：自适应 1-5 秒（高动态场景加密）             │
│  - 每次送 3 帧短窗口（间隔 1 秒，总跨度 3 秒）          │
│  - 无违规 → 仅记录 summary，不触发后续阶段              │
│  - 有违规 → 进入 Layer 2                              │
├─────────────────────────────────────────────────────┤
│  Layer 2: 完整管道（仅违规帧触发）                      │
│  - VisionAgent → RiskAgent → DecisionAgent            │
│  - WorkflowAgent + NotifyAgent 合并为单次调用          │
└─────────────────────────────────────────────────────┘
```

### 关键参数

| 参数 | 当前值 | 建议值 | 说明 |
|------|--------|--------|------|
| 基础采样间隔 | 5s | 2s | 提高采样密度 |
| 滑动窗口大小 | 3帧 | 3帧 | 保持不变 |
| 窗口时间跨度 | 15s | 3s（帧间隔1s） | 缩短跨度增强时序关联 |
| 帧间差分阈值 | 无 | 15%像素变化 | 低于阈值跳过分析 |
| 最大帧数限制 | 120 | 300 | 支持更长视频 |

### 实现要点

1. **`useVideoInspect.ts`**：`FRAME_INTERVAL_S` 从 5 改为 2；`REAL_TIME_DELAY_MS` 从 5000 改为 2000
2. **`video_session.go`**：`getFrameWindow` 保持 3 帧窗口，但帧间隔缩短后实际时间跨度从 15s 降至 6s
3. **新增帧间差分模块**：在 `tools.go` 或独立文件中实现轻量帧差计算，作为 `detect_objects` 的前置过滤

---

## 2. SafeAgent 管道效率优化

### 当前问题
- 5 阶段严格串行，无早退机制
- 安全帧也跑 WorkflowAgent + NotifyAgent（浪费）
- RiskAgent/DecisionAgent 工具多为 echo 脚手架，LLM 调用价值存疑

### 优化方案：条件管道 + 阶段合并

```
当前（每帧必跑）:
  VisionAgent → RiskAgent → DecisionAgent → WorkflowAgent → NotifyAgent
  5 次 LLM 调用，6-17 秒

优化后:
  VisionAgent（VLM 检测）
    ├─ 无违规 → 直接输出 DetectionResult，管道结束（1 次 VLM 调用）
    └─ 有违规 → RiskAgent + DecisionAgent 合并为单次 LLM 调用
                   └─ 高危 → WorkflowAgent（合并 NotifyAgent 功能）
                            最多 3 次 LLM 调用，3-8 秒
```

### 具体改动

#### 2.1 OrchestratorPrompt 增加早退逻辑

在 `prompts.go` 的 `OrchestratorPrompt` 中增加条件分支指令：
- VisionAgent 返回无违规 → 直接输出总结，跳过后续阶段
- 仅当 violations 非空时才进入 RiskAgent

#### 2.2 合并 RiskAgent + DecisionAgent

当前两个 Agent 的工具几乎都是 echo 脚手架（`evaluate_risk`、`determine_strategy`、`assign_person` 都是直接返回输入），LLM 本身就能完成风险定级+策略制定。合并为一个 `AnalysisAgent`，单次 LLM 调用同时输出 `RiskAssessment` + `InspectionDecision`。

#### 2.3 合并 WorkflowAgent + NotifyAgent

两个 Agent 的职责高度关联（创建工单→通知相关人员），合并为 `ClosureAgent`，单次调用完成工单创建+通知发送。

#### 2.4 管道从 5 阶段精简为 3 阶段

| 阶段 | 原 Agent | 新 Agent | LLM 调用次数 |
|------|----------|----------|-------------|
| 1 | VisionAgent | VisionAgent（不变） | 1 次 VLM |
| 2 | RiskAgent + DecisionAgent | AnalysisAgent（合并） | 1 次 LLM |
| 3 | WorkflowAgent + NotifyAgent | ClosureAgent（合并） | 1 次 LLM |

---

## 3. 架构扩展点（为 RTSP 实时流预留）

虽然当前聚焦文件上传，但以下设计决策确保未来平滑过渡：

1. **`VideoSession` 滑动窗口策略已支持流式提交**：`Submit()` 接口无需改动，只需替换帧源（文件解码 → RTSP 拉流）
2. **帧间差分模块独立**：未来可直接对接 RTSP 解码帧，无需经过文件上传路径
3. **管道早退机制天然适配实时场景**：安全帧仅 1 次 VLM 调用，延迟可控在 2-3 秒

---

## 4. 实施步骤

| 步骤 | 文件 | 改动内容 | 优先级 |
|------|------|----------|--------|
| 1 | `useVideoInspect.ts` | 调整 `FRAME_INTERVAL_S=2`, `REAL_TIME_DELAY_MS=2000` | P0 |
| 2 | `prompts.go` | `OrchestratorPrompt` 增加早退逻辑 + 合并后的 Agent prompt | P0 |
| 3 | `types.go` | 新增 `AnalysisResult` 类型（合并 Risk+Decision） | P0 |
| 4 | `subagents.go` | 新增 `NewAnalysisAgent`、`NewClosureAgent`，移除 Risk/Decision/Workflow/Notify | P0 |
| 5 | `tools.go` | 合并工具集，新增帧间差分辅助函数 | P1 |
| 6 | `safeagent.go` | 更新 `buildSpawnSubAgent` switch 分支 | P0 |
| 7 | `video_session.go` | `getFrameWindow` 窗口时间跨度优化 | P1 |
| 8 | 集成测试 | 更新 e2e 测试覆盖新管道 | P1 |

---

## 5. 预期效果

| 指标 | 当前 | 优化后 |
|------|------|--------|
| 安全帧延迟 | 6-17s | 2-3s（仅 VLM） |
| 违规帧延迟 | 6-17s | 3-8s（VLM + 2次LLM） |
| 每帧 LLM 调用 | 5 次 | 1-3 次 |
| 10 分钟视频分析时间 | ~10 分钟（实时等） | ~3-5 分钟 |
| API 成本/帧（安全） | 5 次调用 | 1 次 VLM 调用 |
