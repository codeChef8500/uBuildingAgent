package safeagent

import (
	"github.com/ubuildingagent/backend/agentcore"
	"github.com/ubuildingagent/backend/llmprovider"
)

// newSubAgentLoopConfig builds a shared AgentLoopConfig for sub-agents.
// Each sub-agent gets its own isolated IterationBudget.
func newSubAgentLoopConfig(cfg SubAgentConfig) agentcore.AgentLoopConfig {
	maxIter := cfg.MaxIter
	if maxIter <= 0 {
		maxIter = 10
	}
	return agentcore.AgentLoopConfig{
		Model: cfg.Model,
		StreamOpts: llmprovider.SimpleStreamOptions{
			StreamOptions: llmprovider.StreamOptions{APIKey: cfg.APIKey},
		},
		ToolExecution: agentcore.ToolExecutionSequential,
		Budget:        agentcore.NewIterationBudget(maxIter, 0),
	}
}

// NewVisionAgent creates a VisionAgent with context isolation (empty Messages).
// It registers only vision domain tools; Task tool is intentionally excluded.
func NewVisionAgent(cfg SubAgentConfig) *agentcore.Agent {
	agent := agentcore.NewAgent(newSubAgentLoopConfig(cfg), VisionAgentPrompt)
	for _, t := range buildVisionTools(cfg.VLMModel, cfg.VLMAPIKey, cfg.DetectorEndpoint) {
		agent.AddTool(t)
	}
	return agent
}

// NewAnalysisAgent creates a merged Risk+Decision agent with context isolation.
func NewAnalysisAgent(cfg SubAgentConfig) *agentcore.Agent {
	agent := agentcore.NewAgent(newSubAgentLoopConfig(cfg), AnalysisAgentPrompt)
	for _, t := range buildAnalysisTools() {
		agent.AddTool(t)
	}
	return agent
}

// NewClosureAgent creates a merged Workflow+Notify agent with context isolation.
func NewClosureAgent(cfg SubAgentConfig) *agentcore.Agent {
	agent := agentcore.NewAgent(newSubAgentLoopConfig(cfg), ClosureAgentPrompt)
	for _, t := range buildClosureTools() {
		agent.AddTool(t)
	}
	return agent
}
