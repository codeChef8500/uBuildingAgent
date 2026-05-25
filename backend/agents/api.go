// Package agents — supplementary API types and constants shared across the
// tool and agent layers.
package agents

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
)

// ---------------------------------------------------------------------------
// Message type constants
// ---------------------------------------------------------------------------

const (
	MessageTypeUser      = "user"
	MessageTypeAssistant = "assistant"
	MessageTypeSystem    = "system"
)

// ---------------------------------------------------------------------------
// ContentBlock type constants
// ---------------------------------------------------------------------------

const (
	ContentBlockText       = "text"
	ContentBlockToolResult = "tool_result"
	ContentBlockToolUse    = "tool_use"
	ContentBlockImage      = "image"
)

// ---------------------------------------------------------------------------
// Agent source constants
// ---------------------------------------------------------------------------

const (
	AgentSourceBuiltIn = "built_in"
	AgentSourceUser    = "user"
)

// ForkAgentType is the synthetic subagent type used when forking the current agent.
const ForkAgentType = "__fork__"

// ---------------------------------------------------------------------------
// Stream event type constants
// ---------------------------------------------------------------------------

const (
	EventTextDelta      = "text_delta"
	EventAskUser        = "ask_user"
	EventBrief          = "brief"
	EventPlanModeChange = "plan_mode_change"
	EventDone           = "done"
	EventError          = "error"
)

// ---------------------------------------------------------------------------
// Core streaming / tool call types
// ---------------------------------------------------------------------------

// StreamEvent is a generic event emitted by tools or the model stream.
type StreamEvent struct {
	Type  string          `json:"type"`
	Text  string          `json:"text,omitempty"`
	Error string          `json:"error,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

// ToolUseBlock represents a single tool call as it streams in from the model.
type ToolUseBlock struct {
	ID    string          `json:"id"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input,omitempty"`
}

// ToolDefinition is the API-level tool schema used in LLM requests.
type ToolDefinition struct {
	Name        string      `json:"name"`
	Description string      `json:"description,omitempty"`
	InputSchema interface{} `json:"input_schema,omitempty"`
}

// ---------------------------------------------------------------------------
// Sub-agent types
// ---------------------------------------------------------------------------

// SubAgentParams describes a sub-agent spawn request.
type SubAgentParams struct {
	Description  string `json:"description,omitempty"`
	Prompt       string `json:"prompt"`
	SubagentType string `json:"subagent_type,omitempty"`
	MaxTurns     int    `json:"max_turns,omitempty"`
	Model        string `json:"model,omitempty"`
}

// AgentDefinitions holds runtime metadata about available agent types.
type AgentDefinitions struct {
	AllowedAgentTypes []string `json:"allowed_agent_types,omitempty"`
}

// ToolUseOptions carries optional engine-level configuration forwarded
// through ToolUseContext to tools that need it.
type ToolUseOptions struct {
	AgentDefinitions *AgentDefinitions `json:"agent_definitions,omitempty"`
	// MainLoopModel is the model identifier used for the current agent's main loop.
	// Tools may read or update this to override the model mid-session.
	MainLoopModel string `json:"main_loop_model,omitempty"`
}

// ---------------------------------------------------------------------------
// AskUser types
// ---------------------------------------------------------------------------

// AskUserOption is a single predefined choice in an AskUserQuestion call.
type AskUserOption struct {
	Label   string `json:"label"`
	Preview string `json:"preview,omitempty"`
}

// AskUserPayload is the payload emitted with EventAskUser and passed to the
// ToolUseContext.AskUser handler.
type AskUserPayload struct {
	Question      string          `json:"question"`
	Options       []AskUserOption `json:"options,omitempty"`
	AllowMultiple bool            `json:"allow_multiple,omitempty"`
}

// AskUserResponse is the user's answer returned by the AskUser handler.
type AskUserResponse struct {
	Selected []string `json:"selected,omitempty"`
	Text     string   `json:"text,omitempty"`
}

// ---------------------------------------------------------------------------
// Brief types
// ---------------------------------------------------------------------------

// BriefAttachment is a resolved file attachment forwarded with a brief message.
type BriefAttachment struct {
	Path    string `json:"path"`
	Size    int64  `json:"size,omitempty"`
	IsImage bool   `json:"is_image,omitempty"`
}

// BriefPayload is the payload emitted with EventBrief and stored as tool data.
type BriefPayload struct {
	Message     string            `json:"message"`
	Status      string            `json:"status,omitempty"`
	SentAt      string            `json:"sent_at,omitempty"`
	Attachments []BriefAttachment `json:"attachments,omitempty"`
}

// PlanModeChange is the payload emitted with EventPlanModeChange.
type PlanModeChange struct {
	From    string `json:"from"`
	To      string `json:"to"`
	Summary string `json:"summary,omitempty"`
}

// ---------------------------------------------------------------------------
// File state cache
// ---------------------------------------------------------------------------

// FileState holds the cached read state of a file (path, mtime, size, hash).
type FileState struct {
	Path         string `json:"path"`
	LastModified int64  `json:"last_modified"`
	Size         int64  `json:"size"`
	ContentHash  string `json:"content_hash"`
}

// FileStateCache is the interface for the read-file state tracker.
// Tools use it to record which files have been read (RecordReadState) and
// to gate edits on files that haven't been recently read (HasFreshRead).
type FileStateCache interface {
	Set(path string, st *FileState)
	Get(path string) (*FileState, bool)
}

type fileStateCacheImpl struct {
	mu    sync.RWMutex
	store map[string]*FileState
}

// NewFileStateCache returns a new in-process FileStateCache.
func NewFileStateCache() FileStateCache {
	return &fileStateCacheImpl{store: make(map[string]*FileState)}
}

func (c *fileStateCacheImpl) Set(path string, st *FileState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.store[path] = st
}

func (c *fileStateCacheImpl) Get(path string) (*FileState, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	st, ok := c.store[path]
	return st, ok
}

// ---------------------------------------------------------------------------
// MCP resource types
// ---------------------------------------------------------------------------

// McpResource describes a single MCP server resource.
type McpResource struct {
	URI         string `json:"uri"`
	Server      string `json:"server"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	MimeType    string `json:"mime_type,omitempty"`
}

// McpResourceContent holds the content of a fetched MCP resource.
type McpResourceContent struct {
	URI      string `json:"uri"`
	MimeType string `json:"mime_type,omitempty"`
	Text     string `json:"text,omitempty"`
	Blob     []byte `json:"blob,omitempty"`
}

// McpResourceRegistry is the interface for MCP resource access.
type McpResourceRegistry interface {
	// ListServers returns the names of all connected MCP servers.
	ListServers() []string
	// ListResources returns the resources exposed by the given server.
	// An empty server name aggregates across all servers.
	ListResources(ctx context.Context, server string) ([]McpResource, error)
	// ReadResource fetches the content of a resource by URI from a server.
	ReadResource(ctx context.Context, server, uri string) ([]McpResourceContent, error)
}

// ---------------------------------------------------------------------------
// Streaming tool update types (Phase 4)
// ---------------------------------------------------------------------------

// PartialToolResult carries an intermediate (streaming) result from a tool.
// Tools that support incremental output call ToolUseContext.OnUpdate with
// successive PartialToolResult values as work progresses.
type PartialToolResult struct {
	// Data is the partial result payload (same shape as ToolResult.Data).
	Data interface{} `json:"data,omitempty"`
	// Text is a convenience field for plain-text partial output.
	Text string `json:"text,omitempty"`
}

// ---------------------------------------------------------------------------
// LLM call types
// ---------------------------------------------------------------------------

// CallModelParams is the request payload for a direct LLM call.
type CallModelParams struct {
	Messages        []Message `json:"messages"`
	SystemPrompt    string    `json:"system_prompt,omitempty"`
	Model           string    `json:"model,omitempty"`
	QuerySource     string    `json:"query_source,omitempty"`
	MaxOutputTokens *int      `json:"max_output_tokens,omitempty"`
}

// ---------------------------------------------------------------------------
// Helper functions
// ---------------------------------------------------------------------------

// NewEmptyToolPermissionContext returns a ToolPermissionContext with initialized maps.
func NewEmptyToolPermissionContext() *ToolPermissionContext {
	return &ToolPermissionContext{
		AlwaysAllowRules: make(map[string][]PermissionRule),
		AlwaysDenyRules:  make(map[string][]PermissionRule),
		AlwaysAskRules:   make(map[string][]PermissionRule),
	}
}

// ForkSubagentEnabled reports whether the fork-as-subagent feature is active.
var forkEnabled bool

func ForkSubagentEnabled() bool { return forkEnabled }

// IsInForkChild returns true when the conversation history contains the
// fork-child sentinel string injected by the host's fork boilerplate.
func IsInForkChild(messages []Message) bool {
	for _, m := range messages {
		for _, b := range m.Content {
			if b.Type == ContentBlockText && strings.Contains(b.Text, "FORK_CHILD_SENTINEL") {
				return true
			}
		}
	}
	return false
}
