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
	for _, t := range buildVisionTools(cfg.VLMModel, cfg.VLMAPIKey) {
		agent.AddTool(t)
	}
	return agent
}

// NewRiskAgent creates a RiskAgent with context isolation.
func NewRiskAgent(cfg SubAgentConfig) *agentcore.Agent {
	agent := agentcore.NewAgent(newSubAgentLoopConfig(cfg), RiskAgentPrompt)
	for _, t := range buildRiskTools() {
		agent.AddTool(t)
	}
	return agent
}

// NewDecisionAgent creates a DecisionAgent with context isolation.
func NewDecisionAgent(cfg SubAgentConfig) *agentcore.Agent {
	agent := agentcore.NewAgent(newSubAgentLoopConfig(cfg), DecisionAgentPrompt)
	for _, t := range buildDecisionTools() {
		agent.AddTool(t)
	}
	return agent
}

// NewWorkflowAgent creates a WorkflowAgent with context isolation.
func NewWorkflowAgent(cfg SubAgentConfig) *agentcore.Agent {
	agent := agentcore.NewAgent(newSubAgentLoopConfig(cfg), WorkflowAgentPrompt)
	for _, t := range buildWorkflowTools() {
		agent.AddTool(t)
	}
	return agent
}

// NewNotifyAgent creates a NotifyAgent with context isolation.
func NewNotifyAgent(cfg SubAgentConfig) *agentcore.Agent {
	agent := agentcore.NewAgent(newSubAgentLoopConfig(cfg), NotifyAgentPrompt)
	for _, t := range buildNotifyTools() {
		agent.AddTool(t)
	}
	return agent
}
