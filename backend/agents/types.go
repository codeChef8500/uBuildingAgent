// Package agents defines shared types used across the agent tool layer.
// These types are consumed by the tools/ package and are independent of
// any specific agent implementation or agentcore internals.
package agents

import "encoding/json"

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

	// Extra holds optional per-tool or per-provider metadata.
	Extra map[string]any `json:"extra,omitempty"`
}

// ToolPermissionContext carries the policy rules in force for a given invocation.
// It is used by CheckPermissions and permission-rule evaluation logic.
type ToolPermissionContext struct {
	// Mode is the current permission enforcement mode.
	// Typical values: "default" | "plan" | "bypass" | "auto"
	Mode string `json:"mode"`

	// AlwaysAllowRules lists glob/rule patterns pre-approved by the user or config.
	AlwaysAllowRules []PermissionRule `json:"always_allow_rules,omitempty"`

	// AlwaysDenyRules lists patterns that are unconditionally blocked.
	AlwaysDenyRules []PermissionRule `json:"always_deny_rules,omitempty"`

	// AlwaysAskRules lists patterns that require interactive confirmation.
	AlwaysAskRules []PermissionRule `json:"always_ask_rules,omitempty"`
}

// PermissionRule is a single pattern entry in a permission rule list.
type PermissionRule struct {
	ToolName string `json:"tool_name"`
	Pattern  string `json:"pattern,omitempty"`
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

	// Content holds nested content for compound blocks.
	Content json.RawMessage `json:"content,omitempty"`

	// IsError indicates the content represents a tool error result.
	IsError bool `json:"is_error,omitempty"`
}

// Message is a single message in a conversation, at the agents abstraction layer.
// Tools may produce new messages (e.g. an AgentTool spawning a sub-conversation).
type Message struct {
	Role    string         `json:"role"` // "user" | "assistant" | "system"
	Content []ContentBlock `json:"content"`
}
