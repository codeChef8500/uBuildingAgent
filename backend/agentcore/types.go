// Package agentcore implements the agentic loop: LLM → Tool → LLM.
package agentcore

import (
	"encoding/json"
	"time"

	"github.com/ubuildingagent/backend/llmprovider"
)

// ── Agent Message ─────────────────────────────────────────────────────────

// AgentMessage is a single message in the agent's conversation history.
// It is richer than llmprovider.Message: it carries metadata used by the loop.
type AgentMessage struct {
	ID        string                    `json:"id"`
	Role      llmprovider.Role          `json:"role"`
	Content   []llmprovider.ContentPart `json:"content,omitempty"`
	ToolCalls []llmprovider.ToolCall    `json:"tool_calls,omitempty"`
	Thinking  string                    `json:"thinking,omitempty"`
	Hidden    bool                      `json:"hidden,omitempty"` // excluded from LLM context when true
	Source    string                    `json:"source,omitempty"` // "user"|"assistant"|"system"|"tool"
	Timestamp time.Time                 `json:"timestamp"`
}

// ── Agent Event ───────────────────────────────────────────────────────────

// AgentEventType enumerates the event types emitted by RunAgentLoop.
type AgentEventType string

const (
	AgentEventStart         AgentEventType = "agent_start"
	AgentEventTurnStart     AgentEventType = "turn_start"
	AgentEventTextDelta     AgentEventType = "text_delta"
	AgentEventThinkingDelta AgentEventType = "thinking_delta"
	AgentEventToolStart     AgentEventType = "tool_start"
	AgentEventToolEnd       AgentEventType = "tool_end"
	AgentEventTurnEnd       AgentEventType = "turn_end"
	AgentEventEnd           AgentEventType = "agent_end"
	AgentEventError         AgentEventType = "error"
)

// AgentEvent is emitted on the channel returned by RunAgentLoop / Agent.Prompt.
type AgentEvent struct {
	Type     AgentEventType     `json:"type"`
	Delta    string             `json:"delta,omitempty"`
	ToolCall *ToolCallEvent     `json:"tool_call,omitempty"`
	Message  *AgentMessage      `json:"message,omitempty"`
	Usage    *llmprovider.Usage `json:"usage,omitempty"`
	Err      error              `json:"err,omitempty"`
}

// ToolCallEvent carries tool-call lifecycle data in an AgentEvent.
type ToolCallEvent struct {
	ID        string           `json:"id"`
	Name      string           `json:"name"`
	Arguments json.RawMessage  `json:"arguments,omitempty"`
	Result    *AgentToolResult `json:"result,omitempty"`
	IsError   bool             `json:"is_error,omitempty"`
}

// ── Tool ─────────────────────────────────────────────────────────────────

// ToolExecutionMode controls whether tools in a single turn are run sequentially or in parallel.
type ToolExecutionMode string

const (
	ToolExecutionSequential ToolExecutionMode = "sequential"
	ToolExecutionParallel   ToolExecutionMode = "parallel"
)

// AgentTool is a callable tool registered with the agent.
type AgentTool struct {
	Name          string            `json:"name"`
	Description   string            `json:"description"`
	Parameters    json.RawMessage   `json:"parameters"` // JSON Schema
	Label         string            `json:"label,omitempty"`
	ExecutionMode ToolExecutionMode `json:"execution_mode,omitempty"`

	// Execute is the Go function called when the LLM invokes this tool.
	// Returns AgentToolResult; if Terminate is true the loop exits after this turn.
	Execute func(ctx *ToolExecContext) AgentToolResult `json:"-"`
}

// ToolExecContext is passed to AgentTool.Execute.
type ToolExecContext struct {
	Call     llmprovider.ToolCall
	Args     json.RawMessage // parsed arguments (same as Call.Arguments)
	AgentCtx *AgentContext
}

// AgentToolResult is returned by AgentTool.Execute.
type AgentToolResult struct {
	Content   string `json:"content"`
	Details   string `json:"details,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
	Terminate bool   `json:"terminate,omitempty"` // true = stop the loop after this turn
}

// ── Context ───────────────────────────────────────────────────────────────

// AgentContext holds the live conversation state passed into the agent loop.
type AgentContext struct {
	SystemPrompt string
	Messages     []AgentMessage
	Tools        []AgentTool
}

// ToLLMContext converts an AgentContext to an llmprovider.Context.
// Hidden messages are excluded.  If convertFn is non-nil it is used instead of
// the default field mapping.
func (c *AgentContext) ToLLMContext(convertFn ConvertToLLMFn) llmprovider.Context {
	var msgs []llmprovider.Message
	if convertFn != nil {
		msgs = convertFn(c.Messages)
	} else {
		msgs = DefaultConvertToLLM(c.Messages)
	}

	tools := make([]llmprovider.Tool, 0, len(c.Tools))
	for _, t := range c.Tools {
		tools = append(tools, llmprovider.Tool{
			Name:        t.Name,
			Description: t.Description,
			Parameters:  t.Parameters,
		})
	}

	return llmprovider.Context{
		SystemPrompt: c.SystemPrompt,
		Messages:     msgs,
		Tools:        tools,
	}
}

// ConvertToLLMFn is a custom converter replacing DefaultConvertToLLM.
type ConvertToLLMFn func(messages []AgentMessage) []llmprovider.Message

// ── Loop Config ───────────────────────────────────────────────────────────

// AgentLoopConfig wires all callbacks and resources into the loop.
// Every field is optional (nil/zero = default behaviour).
type AgentLoopConfig struct {
	// Model to use for LLM calls (required).
	Model llmprovider.Model

	// ConvertToLLM overrides the default AgentMessage → llmprovider.Message conversion.
	ConvertToLLM ConvertToLLMFn

	// TransformContext modifies the llmprovider.Context just before each LLM call.
	TransformContext func(ctx llmprovider.Context) llmprovider.Context

	// GetAPIKey returns the API key to use for each LLM call (overrides model.Headers).
	GetAPIKey func() string

	// ShouldStopAfterTurn is called after each inner-loop turn.
	// Return true to stop the loop gracefully (current turn is already complete).
	ShouldStopAfterTurn func(ctx *ShouldStopContext) bool

	// PrepareNextTurn is called before each inner-loop turn to allow mutation
	// of model / thinkingLevel.  Return a copy of config fields to override.
	PrepareNextTurn func(update *AgentLoopTurnUpdate)

	// GetSteeringMessages injects additional "steering" messages after each
	// assistant message (before the next LLM call) within the same outer turn.
	GetSteeringMessages func(msg AgentMessage) []AgentMessage

	// GetFollowUpMessages returns extra user messages to drive the outer loop.
	// Called after inner loop completes.  Empty slice = loop ends.
	GetFollowUpMessages func() []AgentMessage

	// ToolExecution controls sequential or parallel execution within a turn.
	ToolExecution ToolExecutionMode

	// BeforeToolCall is called before each tool execution.
	BeforeToolCall func(ctx *BeforeToolCallContext) BeforeToolCallResult

	// AfterToolCall is called after each tool execution, allowing result patching.
	AfterToolCall func(ctx *AfterToolCallContext) AfterToolCallResult

	// Budget limits iterations and cost.  nil = unlimited.
	Budget *IterationBudget

	// StreamOpts are the base streaming options (APIKey, Temperature, etc.).
	StreamOpts llmprovider.SimpleStreamOptions

	// Session is an optional persistence backend.
	// When set, the loop appends each message and compaction entry automatically.
	Session SessionWriter

	// ContextEngine is an optional context estimator/compactor.
	// When set, compaction is checked before each LLM call.
	ContextEngine ContextEngineIface

	// CompactionSettings configures context compaction (used with ContextEngine).
	CompactionSettings CompactionConfig
}

// SessionWriter is the subset of session operations needed by the loop.
// Using an interface avoids a direct import of the session package.
type SessionWriter interface {
	AppendEntry(entry interface{}) error
}

// ContextEngineIface is the subset of contextmgr.ContextEngine needed by the loop.
type ContextEngineIface interface {
	EstimateContextTokens(messages []AgentMessage) int
	ShouldCompact(tokens int, maxTokens int) bool
	CompactMessages(messages []AgentMessage, maxTokens int) ([]AgentMessage, error)
}

// CompactionConfig holds compaction parameters passed through from AgentLoopConfig.
type CompactionConfig struct {
	MaxTokens    int
	HeadProtectN int
	TailProtectN int
	TargetRatio  float64
}
