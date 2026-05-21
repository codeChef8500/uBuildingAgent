// Package contextmgr provides context estimation and compaction for agent conversations.
// The package is named contextmgr to avoid collision with the standard "context" package.
package contextmgr

import (
	"github.com/ubuildingagent/backend/agentcore"
	"github.com/ubuildingagent/backend/llmprovider"
)

// CompactionSettings controls when and how compaction is triggered.
type CompactionSettings struct {
	// MaxTokens is the context window limit that triggers compaction.
	MaxTokens int

	// HeadProtectN keeps the first N messages intact (prevents losing system context).
	HeadProtectN int

	// TailProtectN keeps the last N messages intact (prevents losing recent context).
	TailProtectN int

	// TargetRatio is the desired token usage after compaction (e.g. 0.5 = 50% of MaxTokens).
	// Default: 0.5
	TargetRatio float64

	// SummarizeFn is called with messages to compress and returns a summary message.
	// If nil, the default compactor drops the messages without summarisation.
	SummarizeFn func(messages []agentcore.AgentMessage) (agentcore.AgentMessage, error)
}

// CompactionResult records the outcome of a compaction operation.
type CompactionResult struct {
	PreTokens      int
	PostTokens     int
	SummaryMessage *agentcore.AgentMessage
	CompactedCount int
	Timestamp      int64 // unix seconds
}

// MessageTokenBreakdown holds per-message token estimates.
type MessageTokenBreakdown struct {
	Index  int
	Role   llmprovider.Role
	Tokens int
}

// ContextEstimate holds the result of a token estimation pass.
type ContextEstimate struct {
	TotalTokens      int
	MessageBreakdown []MessageTokenBreakdown
}

// ContextEngine is the interface implemented by estimators and compactors.
type ContextEngine interface {
	// EstimateTokens returns a token count estimate for the given messages.
	EstimateTokens(messages []agentcore.AgentMessage) ContextEstimate

	// ShouldCompact returns true if compaction should be triggered.
	ShouldCompact(estimate ContextEstimate, settings CompactionSettings) bool

	// Compact reduces the message list according to settings.
	// It returns the compacted messages and a CompactionResult.
	Compact(messages []agentcore.AgentMessage, settings CompactionSettings) ([]agentcore.AgentMessage, CompactionResult, error)
}
