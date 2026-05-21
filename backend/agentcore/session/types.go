// Package session provides conversation persistence: append-only log entries,
// tree-structured forking, and session metadata.
package session

import (
	"encoding/json"
	"time"
)

// EntryType classifies the payload of a SessionEntry.
type EntryType string

const (
	EntryTypeMessage      EntryType = "message"
	EntryTypeToolResult   EntryType = "tool_result"
	EntryTypeCompaction   EntryType = "compaction"
	EntryTypeModelChange  EntryType = "model_change"
)

// SessionEntry is a single immutable record in the session log.
type SessionEntry struct {
	ID        string          `json:"id"`
	ParentID  string          `json:"parent_id,omitempty"` // empty for root entries
	Timestamp time.Time       `json:"timestamp"`
	Type      EntryType       `json:"type"`
	Payload   json.RawMessage `json:"payload"`
}

// SessionMetadata summarises a session for listing.
type SessionMetadata struct {
	ID           string    `json:"id"`
	Title        string    `json:"title,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	MessageCount int       `json:"message_count"`
	TotalTokens  int       `json:"total_tokens"`
	ModelID      string    `json:"model_id,omitempty"`
}

// SessionContext is the reconstructed conversation context built from a
// linear path of entries (root → target).
type SessionContext struct {
	Messages         []json.RawMessage `json:"messages"`           // serialised AgentMessages
	SystemPrompt     string            `json:"system_prompt"`
	CompactionResult *CompactionRef    `json:"compaction_result,omitempty"`
}

// CompactionRef references a prior compaction entry embedded in the session.
type CompactionRef struct {
	EntryID   string    `json:"entry_id"`
	Timestamp time.Time `json:"timestamp"`
	Summary   string    `json:"summary,omitempty"`
	PreTokens int       `json:"pre_tokens"`
	PostTokens int      `json:"post_tokens"`
}

// MessagePayload is the concrete payload for EntryTypeMessage entries.
type MessagePayload struct {
	Message json.RawMessage `json:"message"` // serialised AgentMessage
}

// ToolResultPayload is the payload for EntryTypeToolResult entries.
type ToolResultPayload struct {
	ToolCallID string `json:"tool_call_id"`
	Content    string `json:"content"`
	IsError    bool   `json:"is_error,omitempty"`
}

// CompactionPayload is the payload for EntryTypeCompaction entries.
type CompactionPayload struct {
	PreTokens  int    `json:"pre_tokens"`
	PostTokens int    `json:"post_tokens"`
	Summary    string `json:"summary,omitempty"`
}

// ModelChangePayload is the payload for EntryTypeModelChange entries.
type ModelChangePayload struct {
	OldModelID string `json:"old_model_id"`
	NewModelID string `json:"new_model_id"`
}

// MarshalPayload marshals v to JSON and returns a ready-to-embed json.RawMessage.
func MarshalPayload(v any) (json.RawMessage, error) {
	return json.Marshal(v)
}
