// Package agents defines shared types used across the agent tool layer.
// These types are consumed by the tools/ package and are independent of
// any specific agent implementation or agentcore internals.
package agents

import (
	"context"
	"encoding/json"
)

// ToolUseContext carries per-call contextual information for tool execution.
// It is passed into Tool.Call(), Tool.CheckPermissions(), and Tool.ValidateInput().
type ToolUseContext struct {
	// SessionID is the unique identifier of the current agent session.
	SessionID string `json:"session_id,omitempty"`

	// AgentID identifies the sub-agent (if any) that invoked this tool.
	AgentID string `json:"agent_id,omitempty"`

	// IsNonInteractive indicates the session has no human in the loop.
	// Tools should avoid prompting for confirmation in this mode.
	IsNonInteractive bool `json:"is_non_interactive,omitempty"`

	// WorkingDirectory is the project root as understood by this session.
	WorkingDirectory string `json:"working_dir,omitempty"`

	// PlanMode is the current plan mode value ("normal" | "plan").
	PlanMode string `json:"plan_mode,omitempty"`

	// Extra holds optional per-tool or per-provider metadata.
	Extra map[string]any `json:"extra,omitempty"`

	// Ctx is the request context for cancellation propagation.
	Ctx context.Context `json:"-"`

	// Messages holds the current conversation history (used by fork detection).
	Messages []Message `json:"-"`

	// Options carries optional engine-level configuration.
	Options ToolUseOptions `json:"-"`

	// SpawnSubAgent dispatches a sub-agent task and returns its text result.
	SpawnSubAgent func(ctx context.Context, p SubAgentParams) (string, error) `json:"-"`

	// AskUser presents a question to the human operator and waits for a response.
	AskUser func(ctx context.Context, payload AskUserPayload) (AskUserResponse, error) `json:"-"`

	// EmitEvent broadcasts a stream event to the host (e.g. for UI updates).
	EmitEvent func(event StreamEvent) `json:"-"`

	// ReadFileState caches recent file reads to gate edits on stale state.
	ReadFileState FileStateCache `json:"-"`

	// TaskGraph is an optional task-graph manager (opaque interface).
	TaskGraph interface{} `json:"-"`

	// TaskManager is an optional background-task manager (opaque interface).
	TaskManager interface{} `json:"-"`

	// TodoStore is an optional todo/task-list store (opaque interface).
	TodoStore interface{} `json:"-"`

	// McpResources provides access to MCP server resources.
	McpResources McpResourceRegistry `json:"-"`

	// OnUpdate, when non-nil, is called by tools that support streaming partial
	// results. Each call delivers an intermediate PartialToolResult so the host
	// can update the UI before the final result is ready (Phase 4).
	OnUpdate func(partial *PartialToolResult) `json:"-"`
}

// ToolPermissionContext carries the policy rules in force for a given invocation.
// It is used by CheckPermissions and permission-rule evaluation logic.
type ToolPermissionContext struct {
	// Mode is the current permission enforcement mode.
	// Typical values: "default" | "plan" | "bypass" | "auto"
	Mode string `json:"mode"`

	// AlwaysAllowRules maps tool names to pre-approved rule patterns.
	AlwaysAllowRules map[string][]PermissionRule `json:"always_allow_rules,omitempty"`

	// AlwaysDenyRules maps tool names to unconditionally blocked rule patterns.
	AlwaysDenyRules map[string][]PermissionRule `json:"always_deny_rules,omitempty"`

	// AlwaysAskRules maps tool names to patterns requiring interactive confirmation.
	AlwaysAskRules map[string][]PermissionRule `json:"always_ask_rules,omitempty"`
}

// PermissionRule is a single pattern entry in a permission rule list.
type PermissionRule struct {
	Tool    string `json:"tool"`
	Pattern string `json:"pattern,omitempty"`
}

// ContentBlock represents a single piece of structured content returned by a tool.
// It mirrors the Anthropic API content block union type.
type ContentBlock struct {
	// Type identifies the block kind: "text" | "image" | "tool_use" | "tool_result"
	Type string `json:"type"`

	// Text holds the text content (when Type == "text").
	Text string `json:"text,omitempty"`

	// ToolUseID is the correlated tool-use identifier (when Type == "tool_result").
	ToolUseID string `json:"tool_use_id,omitempty"`

	// Content holds the content of a tool_result block (string or structured data).
	Content interface{} `json:"content,omitempty"`

	// IsError indicates the content represents a tool error result.
	IsError bool `json:"is_error,omitempty"`

	// Name is the tool name for tool_use blocks.
	Name string `json:"name,omitempty"`

	// Input holds the tool input for tool_use blocks.
	Input json.RawMessage `json:"input,omitempty"`
}

// Message is a single message in a conversation, at the agents abstraction layer.
// Tools may produce new messages (e.g. an AgentTool spawning a sub-conversation).
type Message struct {
	// Type is the message role: "user" | "assistant" | "system".
	Type string `json:"type,omitempty"`

	// Role is an alias for Type kept for backward compatibility.
	Role string `json:"role,omitempty"`

	// UUID is the unique identifier for this message.
	UUID string `json:"uuid,omitempty"`

	// Content holds the structured content blocks.
	Content []ContentBlock `json:"content"`

	// SourceToolAssistantUUID links a tool_result message back to the assistant
	// message that contained the tool_use call.
	SourceToolAssistantUUID string `json:"source_tool_assistant_uuid,omitempty"`

	// ToolUseResult holds the raw text result for tool_result messages.
	ToolUseResult string `json:"tool_use_result,omitempty"`

	// AttachmentMessages carries any inline attachment messages.
	AttachmentMessages []Message `json:"attachment_messages,omitempty"`
}

// AgentDefinition describes a sub-agent type available to the Task tool.
// It controls which tools the agent may access and how permissions are enforced.
type AgentDefinition struct {
	// Name is the unique identifier for this agent type (e.g. "code", "research").
	Name string `json:"name"`

	// AgentType is the type key shown in Task tool listings (may differ from Name).
	AgentType string `json:"agent_type,omitempty"`

	// WhenToUse is a short description shown in Task tool agent listings.
	WhenToUse string `json:"when_to_use,omitempty"`

	// Description is a human-readable summary of what the agent does.
	Description string `json:"description,omitempty"`

	// Source indicates the origin of the definition ("built_in" | "user").
	Source string `json:"source,omitempty"`

	// BuiltIn marks this as a first-party (built-in) agent definition.
	BuiltIn bool `json:"built_in,omitempty"`

	// PermissionMode overrides the default permission enforcement mode.
	// Typical values: "default" | "plan" | "bypass" | "auto"
	PermissionMode string `json:"permission_mode,omitempty"`

	// Tools is the agent's allow-list for tool access.
	// Empty slice or ["*"] means all tools are permitted (subject to disallow list).
	Tools []string `json:"tools,omitempty"`

	// DisallowedTools lists tools explicitly denied to this agent.
	DisallowedTools []string `json:"disallowed_tools,omitempty"`
}

// IsBuiltIn reports whether this is a first-party agent definition.
func (a *AgentDefinition) IsBuiltIn() bool {
	if a == nil {
		return false
	}
	return a.BuiltIn
}

// AllAgentDisallowedTools is the set of tools that sub-agents can never access,
// regardless of their allow list or built-in status.
// Mirrors claude-code's ALL_AGENT_DISALLOWED_TOOLS constant.
var AllAgentDisallowedTools = map[string]struct{}{
	"Task":            {},
	"ExitPlanMode":    {},
	"AskUserQuestion": {},
}

// CustomAgentDisallowedTools extends AllAgentDisallowedTools for non-built-in
// (user-defined) agents. These tools are stripped in addition to the baseline set.
var CustomAgentDisallowedTools = map[string]struct{}{}

// AsyncAgentAllowedTools is the allow-set for async (background) agents.
// Only tools in this set are accessible; all others are stripped.
// Mirrors claude-code's ASYNC_AGENT_ALLOWED_TOOLS constant.
var AsyncAgentAllowedTools = map[string]struct{}{
	"Bash":         {},
	"Edit":         {},
	"ExitWorktree": {},
	"Glob":         {},
	"Grep":         {},
	"LS":           {},
	"Read":         {},
	"WebFetch":     {},
	"WebSearch":    {},
	"Write":        {},
}

// InProcessTeammateAllowedTools is an additional carve-out for in-process
// teammates that need task-graph and messaging tools beyond AsyncAgentAllowedTools.
var InProcessTeammateAllowedTools = map[string]struct{}{
	"SendMessage":  {},
	"TaskCreate":   {},
	"TaskUpdate":   {},
	"TaskComplete": {},
}
