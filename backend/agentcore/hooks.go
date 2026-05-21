package agentcore

import (
	"encoding/json"

	"github.com/ubuildingagent/backend/llmprovider"
)

// ── BeforeToolCall ────────────────────────────────────────────────────────

// BeforeToolCallContext is passed to AgentLoopConfig.BeforeToolCall.
type BeforeToolCallContext struct {
	AssistantMessage AgentMessage
	ToolCallID       string
	ToolName         string
	Args             json.RawMessage
	AgentCtx         *AgentContext
}

// BeforeToolCallResult is returned by AgentLoopConfig.BeforeToolCall.
// Set Block=true to skip execution of this tool call entirely.
type BeforeToolCallResult struct {
	Block  bool   // true = skip this tool call
	Reason string // human-readable reason for blocking (surfaced in tool_end event)
}

// ── AfterToolCall ─────────────────────────────────────────────────────────

// AfterToolCallContext is passed to AgentLoopConfig.AfterToolCall.
type AfterToolCallContext struct {
	AssistantMessage AgentMessage
	ToolCallID       string
	ToolName         string
	Args             json.RawMessage
	Result           AgentToolResult
	IsError          bool
	AgentCtx         *AgentContext
}

// AfterToolCallResult is returned by AgentLoopConfig.AfterToolCall.
// Nil pointer fields mean "keep the original value from AgentToolResult".
//
// Merge semantics:
//   - Content non-empty → replaces Result.Content
//   - IsError non-nil   → replaces Result.IsError
//   - Terminate non-nil → replaces Result.Terminate
type AfterToolCallResult struct {
	Content   string
	Details   string
	IsError   *bool
	Terminate *bool
}

// Apply merges r on top of orig and returns the updated AgentToolResult.
func (r AfterToolCallResult) Apply(orig AgentToolResult) AgentToolResult {
	if r.Content != "" {
		orig.Content = r.Content
	}
	if r.Details != "" {
		orig.Details = r.Details
	}
	if r.IsError != nil {
		orig.IsError = *r.IsError
	}
	if r.Terminate != nil {
		orig.Terminate = *r.Terminate
	}
	return orig
}

// ── ShouldStopAfterTurn ───────────────────────────────────────────────────

// ShouldStopContext is passed to AgentLoopConfig.ShouldStopAfterTurn.
type ShouldStopContext struct {
	AssistantMessage AgentMessage
	ToolResults      []AgentToolResult
	AgentCtx         *AgentContext
	NewMessages      []AgentMessage // messages appended this turn
}

// ── PrepareNextTurn ───────────────────────────────────────────────────────

// AgentLoopTurnUpdate is passed to AgentLoopConfig.PrepareNextTurn.
// Mutate fields to override the model / thinking level for the upcoming turn.
type AgentLoopTurnUpdate struct {
	AgentCtx      *AgentContext
	Model         *llmprovider.Model // pointer: mutate to override
	ThinkingLevel *llmprovider.ThinkingLevel
}
