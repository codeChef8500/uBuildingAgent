package safeagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ubuildingagent/backend/agentcore"
	"github.com/ubuildingagent/backend/agents"
	"github.com/ubuildingagent/backend/llmprovider"
	tool "github.com/ubuildingagent/backend/tools"
	"github.com/ubuildingagent/backend/tools/agenttool"
)

// New builds and returns a SafeAgent orchestrator.
//
// Assembly:
//  1. Build AgentDefinitions for the 5 sub-agent types.
//  2. Build the SpawnSubAgent function that switches on subagent_type.
//  3. Build the Task agentcore.AgentTool directly (bypassing tools/adapter.go)
//     so that SpawnSubAgent is injected into the Execute closure.
//  4. Construct the orchestrator agentcore.Agent and register the Task tool.
func New(cfg Config) *agentcore.Agent {
	if cfg.OrchestratorMaxIter <= 0 {
		cfg.OrchestratorMaxIter = 20
	}
	if cfg.SubAgentMaxIter <= 0 {
		cfg.SubAgentMaxIter = 10
	}

	subCfg := SubAgentConfig{
		Model:            cfg.Model,
		APIKey:           cfg.APIKey,
		MaxIter:          cfg.SubAgentMaxIter,
		VLMModel:         cfg.VLMModel,
		VLMAPIKey:        cfg.VLMAPIKey,
		DetectorEndpoint: cfg.DetectorEndpoint,
	}

	agentDefs := buildAgentDefinitions()
	spawnFn := buildSpawnSubAgent(subCfg)
	taskTool := buildTaskTool(spawnFn, agentDefs)

	loopCfg := agentcore.AgentLoopConfig{
		Model: cfg.Model,
		StreamOpts: llmprovider.SimpleStreamOptions{
			StreamOptions: llmprovider.StreamOptions{APIKey: cfg.APIKey},
		},
		ToolExecution: agentcore.ToolExecutionSequential,
		Budget:        agentcore.NewIterationBudget(cfg.OrchestratorMaxIter, 0),
	}

	orchestrator := agentcore.NewAgent(loopCfg, OrchestratorPrompt)
	orchestrator.AddTool(taskTool)
	return orchestrator
}

// buildTaskTool constructs an agentcore.AgentTool that wraps agenttool.New().
//
// IMPORTANT: This deliberately bypasses tools/adapter.go (ToAgentTool) because
// tools/adapter.go:toolUseContextFromExec does not populate SpawnSubAgent
// (agentcore.ToolExecContext has no such field by design). The Execute closure
// here constructs agents.ToolUseContext manually and injects spawnFn directly.
func buildTaskTool(
	spawnFn func(context.Context, agents.SubAgentParams) (string, error),
	defs []*agents.AgentDefinition,
) agentcore.AgentTool {
	impl := agenttool.New(
		agenttool.WithAllowedSubagentTypes(
			AgentTypeVision, AgentTypeAnalysis, AgentTypeClosure,
		),
		agenttool.WithAgentCatalog(func() []*agents.AgentDefinition { return defs }),
	)

	schema, _ := json.Marshal(impl.InputSchema())

	return agentcore.AgentTool{
		Name:          impl.Name(),
		Description:   "派发专项子Agent执行安全巡检管道中的单个阶段任务",
		Parameters:    schema,
		ExecutionMode: agentcore.ToolExecutionSequential,
		DynamicDescription: func(_ *agentcore.AgentContext) string {
			return impl.Prompt(tool.PromptOptions{AgentToolIsCoordinator: true})
		},
		Execute: func(texCtx *agentcore.ToolExecContext) agentcore.AgentToolResult {
			toolCtx := &agents.ToolUseContext{
				Ctx:           texCtx.Ctx,
				SpawnSubAgent: spawnFn,
			}
			result, err := impl.Call(texCtx.Ctx, texCtx.Args, toolCtx)
			if err != nil {
				return agentcore.AgentToolResult{
					Content: fmt.Sprintf("Task 工具执行失败: %v", err),
					IsError: true,
				}
			}
			b, _ := json.Marshal(result.Data)
			return agentcore.AgentToolResult{Content: string(b)}
		},
	}
}

// buildSpawnSubAgent returns a SpawnSubAgent function that routes by subagent_type
// to the corresponding sub-agent constructor.
//
// Each invocation creates a fresh agentcore.Agent (empty Messages) to guarantee
// context isolation — equivalent to hermes-agent's "fresh conversation" and
// opencode's session isolation per Task call.
func buildSpawnSubAgent(cfg SubAgentConfig) func(context.Context, agents.SubAgentParams) (string, error) {
	return func(ctx context.Context, p agents.SubAgentParams) (string, error) {
		var sub *agentcore.Agent

		switch p.SubagentType {
		case AgentTypeVision:
			sub = NewVisionAgent(cfg)
		case AgentTypeAnalysis:
			sub = NewAnalysisAgent(cfg)
		case AgentTypeClosure:
			sub = NewClosureAgent(cfg)
		default:
			return "", fmt.Errorf("safeagent: unknown subagent_type %q", p.SubagentType)
		}

		// Prompt the sub-agent with the stage input as the first (and only) user message.
		// Sub-agent starts with empty history — full context isolation.
		ch := sub.Prompt(ctx, p.Prompt)

		var sb strings.Builder
		for ev := range ch {
			switch ev.Type {
			case agentcore.AgentEventTextDelta:
				sb.WriteString(ev.Delta)
			case agentcore.AgentEventError:
				if ev.Err != nil {
					return "", fmt.Errorf("safeagent sub-agent %q error: %w", p.SubagentType, ev.Err)
				}
			}
		}
		return sb.String(), nil
	}
}
