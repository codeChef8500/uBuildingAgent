// Package agentcore implements the agentic loop: LLM → Tool → LLM.
package agentcore

import (
	"context"
	"encoding/json"
	"time"

	"github.com/ubuildingagent/backend/llmprovider"
)

// ── Agent Message ─────────────────────────────────────────────────────────

// AgentMessage is a single message in the agent's conversation history.
// It is richer than llmprovider.Message: it carries metadata used by the loop.
type AgentMessage struct {
	ID           string                    `json:"id"`
	Role         llmprovider.Role          `json:"role"`
	Content      []llmprovider.ContentPart `json:"content,omitempty"`
	ToolCalls    []llmprovider.ToolCall    `json:"tool_calls,omitempty"`
	Thinking     string                    `json:"thinking,omitempty"`
	Hidden       bool                      `json:"hidden,omitempty"`        // excluded from LLM context when true
	Source       string                    `json:"source,omitempty"`        // "user"|"assistant"|"system"|"tool"
	IsError      bool                      `json:"is_error,omitempty"`      // true for synthetic error messages
	ErrorMessage string                    `json:"error_message,omitempty"` // error detail when IsError is true
	Timestamp    time.Time                 `json:"timestamp"`
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
	AgentEventToolUpdate    AgentEventType = "tool_update"
	AgentEventTurnEnd       AgentEventType = "turn_end"
	AgentEventContextPatch  AgentEventType = "context_patch"
	AgentEventEnd           AgentEventType = "agent_end"
	AgentEventError         AgentEventType = "error"

	// Message lifecycle events (P1-4): emitted during streaming for live partial updates.
	AgentEventMessageStart  AgentEventType = "message_start"  // first content arrived; Message is partial
	AgentEventMessageUpdate AgentEventType = "message_update" // delta applied; Message is updated partial
	AgentEventMessageEnd    AgentEventType = "message_end"    // streaming complete; Message is final
)

// AgentEvent is emitted on the channel returned by RunAgentLoop / Agent.Prompt.
type AgentEvent struct {
	Type     AgentEventType     `json:"type"`
	Delta    string             `json:"delta,omitempty"`
	ToolCall *ToolCallEvent     `json:"tool_call,omitempty"`
	Message  *AgentMessage      `json:"message,omitempty"`
	Messages []AgentMessage     `json:"messages,omitempty"`
	Usage    *llmprovider.Usage `json:"usage,omitempty"`
	Err      error              `json:"err,omitempty"`
}

// ToolCallEvent carries tool-call lifecycle data in an AgentEvent.
type ToolCallEvent struct {
	ID            string           `json:"id"`
	Name          string           `json:"name"`
	Arguments     json.RawMessage  `json:"arguments,omitempty"`
	Result        *AgentToolResult `json:"result,omitempty"`
	PartialResult *AgentToolResult `json:"partial_result,omitempty"`
	IsError       bool             `json:"is_error,omitempty"`
}

// ── Tool ─────────────────────────────────────────────────────────────────

// ToolExecutionMode controls whether tools in a single turn are run sequentially or in parallel.
type ToolExecutionMode string

const (
	ToolExecutionSequential ToolExecutionMode = "sequential"
	ToolExecutionParallel   ToolExecutionMode = "parallel"
)

// ToolValidation is the result of AgentTool.ValidateInput.
type ToolValidation struct {
	Valid   bool   `json:"valid"`
	Message string `json:"message,omitempty"`
}

// ToolPermissionBehavior is the outcome of AgentTool.CheckPermission.
type ToolPermissionBehavior string

const (
	ToolPermissionAllow ToolPermissionBehavior = "allow"
	ToolPermissionDeny  ToolPermissionBehavior = "deny"
	ToolPermissionAsk   ToolPermissionBehavior = "ask"
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

	// PrepareArguments is an optional hook called before ValidateInput.
	// Use it to normalise raw LLM args (e.g. fix field-name differences, add defaults).
	// Return nil to keep the original args unchanged.
	// nil = skip pre-processing.
	PrepareArguments func(args json.RawMessage) json.RawMessage `json:"-"`

	// ValidateInput is an optional hook called before Execute.
	// Return Valid=false to abort execution and surface Message as the tool result.
	// nil = skip validation.
	ValidateInput func(args json.RawMessage) *ToolValidation `json:"-"`

	// CheckPermission is an optional hook called after ValidateInput.
	// Return ToolPermissionDeny to block; ToolPermissionAsk to surface for user
	// confirmation via BeforeToolCall; ToolPermissionAllow to proceed.
	// nil = always allow.
	CheckPermission func(args json.RawMessage) ToolPermissionBehavior `json:"-"`

	// DynamicDescription is an optional function that returns a runtime-computed
	// description (e.g. the current skill list for a skill tool).  When non-nil
	// it replaces Description in every ToLLMContext() call.
	DynamicDescription func(conv *AgentContext) string `json:"-"`
}

// ToolExecContext is passed to AgentTool.Execute.
type ToolExecContext struct {
	Ctx      context.Context // inherits the agent loop's context; tools should honour cancellation
	Call     llmprovider.ToolCall
	Args     json.RawMessage // parsed arguments (same as Call.Arguments)
	AgentCtx *AgentContext

	// OnUpdate, when non-nil, is called by tools to emit an intermediate
	// (streaming) result before the final AgentToolResult is returned.
	// The loop emits AgentEventToolUpdate for each call.
	OnUpdate func(partial *AgentToolResult) `json:"-"`
}

// AgentToolResult is returned by AgentTool.Execute.
type AgentToolResult struct {
	Content   string `json:"content"`
	Details   string `json:"details,omitempty"`
	IsError   bool   `json:"is_error,omitempty"`
	Terminate bool   `json:"terminate,omitempty"` // true = stop the loop after this turn

	// ContentParts, when non-nil, replaces the plain Content string as the tool-result
	// payload sent to the LLM.  Supports rich multi-modal content (text + images).
	// nil = fall back to Content string.
	ContentParts []llmprovider.ContentPart `json:"content_parts,omitempty"`

	// ContextModifier is an optional function that mutates the AgentContext
	// after the tool result is appended.  Use it to inject new messages or
	// modify the conversation state (e.g. when a skill tool loads new content).
	// nil = no mutation.
	ContextModifier func(conv *AgentContext) `json:"-"`
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
// DynamicDescription on any AgentTool is evaluated here and replaces the
// static Description field in the outgoing LLM context.
func (c *AgentContext) ToLLMContext(convertFn ConvertToLLMFn) llmprovider.Context {
	var msgs []llmprovider.Message
	if convertFn != nil {
		msgs = convertFn(c.Messages)
	} else {
		msgs = DefaultConvertToLLM(c.Messages)
	}

	tools := make([]llmprovider.Tool, 0, len(c.Tools))
	for _, t := range c.Tools {
		desc := t.Description
		if t.DynamicDescription != nil {
			desc = t.DynamicDescription(c)
		}
		tools = append(tools, llmprovider.Tool{
			Name:        t.Name,
			Description: desc,
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

	// Memory is an optional memory backend integration.
	// When set, RecallContext is called before each LLM call and SyncTurn is
	// called asynchronously after each completed turn.
	// nil = no memory module.
	Memory MemoryProvider

	// Skills is an optional skills backend integration.
	// When set, ToolSchemas() is expected to be appended to the agent's tool list
	// during agent initialisation by the caller (not by agentcore).
	// nil = no skills module.
	Skills SkillProvider
}

// SessionWriter is the subset of session operations needed by the loop.
// Using an interface avoids a direct import of the session package.
type SessionWriter interface {
	AppendEntry(entry interface{}) error
}

// ── Memory / Skills integration interfaces ────────────────────────────────

// MemoryProvider is the integration point for memory backends.
// Implementations live in backend/memory/; agentcore only sees this interface.
// agentcore never constructs system prompts — that responsibility belongs to
// the concrete agent layer (e.g. codeagent).  SystemPromptBlock() is defined
// on the concrete memory type, not here.
type MemoryProvider interface {
	// RecallContext returns dynamic context relevant to the current user query.
	// The result is injected as a Hidden=true system message before each LLM
	// call so the model can see relevant memories without polluting history.
	RecallContext(ctx context.Context, query string) string

	// SyncTurn persists a completed turn asynchronously.
	// Called after tool results are collected; must not block the main loop.
	SyncTurn(ctx context.Context, userMsg, assistantMsg AgentMessage)

	// ToolSchemas returns AgentTools exposed to the LLM (e.g. read_memory,
	// write_memory).  Return nil when no tools should be exposed.
	ToolSchemas() []AgentTool
}

// SkillProvider is the integration point for skills backends.
// agentcore does not touch system prompt content for skills — the caller
// assembles the prompt and passes the resolved tool list to NewAgent().
type SkillProvider interface {
	// ListSkills returns metadata of all currently available skills.
	ListSkills() []SkillMeta

	// LoadSkill returns the prompt content of a named skill.
	// Called by the invoke_skill AgentTool's Execute function.
	LoadSkill(name string) (content string, err error)

	// ToolSchemas returns AgentTools exposed to the LLM
	// (e.g. list_skills, invoke_skill).
	ToolSchemas() []AgentTool
}

// SkillMeta is the lightweight descriptor returned by SkillProvider.ListSkills.
type SkillMeta struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Tags        []string `json:"tags,omitempty"`
}

// ── Queue (P2-8) ─────────────────────────────────────────────────────────

// QueueMode controls how many messages are drained from a MessageQueue at once.
//
//   - QueueModeAll: drain all queued messages in one call.
//   - QueueModeOneAtATime: drain only the oldest message, leaving the rest for later.
type QueueMode string

const (
	QueueModeAll        QueueMode = "all"
	QueueModeOneAtATime QueueMode = "one-at-a-time"
)

// MessageQueue is a simple FIFO queue of AgentMessages with configurable drain semantics.
type MessageQueue struct {
	Mode     QueueMode
	messages []AgentMessage
}

// NewMessageQueue creates an empty queue with the given drain mode.
func NewMessageQueue(mode QueueMode) *MessageQueue {
	if mode == "" {
		mode = QueueModeOneAtATime
	}
	return &MessageQueue{Mode: mode}
}

// Enqueue appends a message to the queue.
func (q *MessageQueue) Enqueue(msg AgentMessage) {
	q.messages = append(q.messages, msg)
}

// Drain returns messages according to the queue mode and removes them.
func (q *MessageQueue) Drain() []AgentMessage {
	if len(q.messages) == 0 {
		return nil
	}
	if q.Mode == QueueModeAll {
		out := q.messages
		q.messages = nil
		return out
	}
	out := []AgentMessage{q.messages[0]}
	q.messages = q.messages[1:]
	return out
}

// Clear removes all queued messages.
func (q *MessageQueue) Clear() {
	q.messages = nil
}

// Len returns the number of queued messages.
func (q *MessageQueue) Len() int { return len(q.messages) }

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
