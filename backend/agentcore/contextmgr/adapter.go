package contextmgr

import (
	"github.com/ubuildingagent/backend/agentcore"
)

// LoopAdapter bridges contextmgr.ContextEngine to agentcore.ContextEngineIface
// so that a DefaultCompactor or CompactionPipeline can be plugged directly into
// AgentLoopConfig.ContextEngine without a circular import.
type LoopAdapter struct {
	engine   ContextEngine
	settings CompactionSettings
}

// NewLoopAdapter wraps a ContextEngine with fixed CompactionSettings.
// Pass the result to AgentLoopConfig.ContextEngine.
func NewLoopAdapter(engine ContextEngine, settings CompactionSettings) *LoopAdapter {
	return &LoopAdapter{engine: engine, settings: settings}
}

// EstimateContextTokens implements agentcore.ContextEngineIface.
func (a *LoopAdapter) EstimateContextTokens(messages []agentcore.AgentMessage) int {
	return a.engine.EstimateTokens(messages).TotalTokens
}

// ShouldCompact implements agentcore.ContextEngineIface.
func (a *LoopAdapter) ShouldCompact(tokens int, maxTokens int) bool {
	est := ContextEstimate{TotalTokens: tokens}
	s := a.settings
	s.MaxTokens = maxTokens
	return a.engine.ShouldCompact(est, s)
}

// CompactMessages implements agentcore.ContextEngineIface.
func (a *LoopAdapter) CompactMessages(messages []agentcore.AgentMessage, maxTokens int) ([]agentcore.AgentMessage, error) {
	s := a.settings
	s.MaxTokens = maxTokens
	compacted, _, err := a.engine.Compact(messages, s)
	return compacted, err
}
