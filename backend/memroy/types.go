// Package memroy provides the MemoryProvider implementation for agentcore.
//
// Design overview:
//   - Implements agentcore.MemoryProvider interface.
//   - RecallContext performs vector or keyword search over stored facts/turns
//     and returns a formatted string injected before each LLM call.
//   - SyncTurn persists a completed user→assistant turn asynchronously.
//   - ToolSchemas exposes optional LLM-callable tools (read_memory, write_memory).
//
// NOTE: This is a skeleton. Fill in the Store, Recall, and Sync methods
// with a real storage backend (e.g. SQLite, Qdrant, in-memory vector store).
package memroy

import (
	"context"

	"github.com/ubuildingagent/backend/agentcore"
)

// Provider is the concrete memory backend that satisfies agentcore.MemoryProvider.
type Provider struct {
	// store is the underlying storage backend (pluggable).
	store Store
}

// Store is the minimal persistence contract required by Provider.
// Swap in any concrete implementation (SQLite, Qdrant, etc.).
type Store interface {
	// Search returns relevant memory fragments for the given query string.
	Search(ctx context.Context, query string, topK int) ([]MemoryEntry, error)

	// Append persists a new memory entry.
	Append(ctx context.Context, entry MemoryEntry) error
}

// MemoryEntry is a single stored memory fragment.
type MemoryEntry struct {
	ID      string `json:"id"`
	Content string `json:"content"`
	Role    string `json:"role,omitempty"` // "user" | "assistant"
}

// NewProvider creates a Provider backed by the given Store.
func NewProvider(store Store) *Provider {
	return &Provider{store: store}
}

// RecallContext implements agentcore.MemoryProvider.
// It searches the store for entries relevant to query and returns a
// formatted context block the loop injects before the LLM call.
func (p *Provider) RecallContext(ctx context.Context, query string) string {
	if p.store == nil || query == "" {
		return ""
	}
	entries, err := p.store.Search(ctx, query, 5)
	if err != nil || len(entries) == 0 {
		return ""
	}
	var sb string
	sb = "<memory>\n"
	for _, e := range entries {
		sb += e.Content + "\n"
	}
	sb += "</memory>"
	return sb
}

// SyncTurn implements agentcore.MemoryProvider.
// It persists the user and assistant messages from the completed turn.
func (p *Provider) SyncTurn(ctx context.Context, userMsg, assistantMsg agentcore.AgentMessage) {
	if p.store == nil {
		return
	}
	for _, msg := range []agentcore.AgentMessage{userMsg, assistantMsg} {
		if msg.ID == "" {
			continue
		}
		text := extractText(msg)
		if text == "" {
			continue
		}
		_ = p.store.Append(ctx, MemoryEntry{
			ID:      msg.ID,
			Content: text,
			Role:    string(msg.Role),
		})
	}
}

// ToolSchemas implements agentcore.MemoryProvider.
// Returns nil — memory tools are not exposed to the LLM by default.
// Override this in a sub-type to expose read_memory / write_memory tools.
func (p *Provider) ToolSchemas() []agentcore.AgentTool {
	return nil
}

// SystemPromptBlock returns a static description of the memory module for use
// in system prompts.  Called by the concrete agent layer — NOT by agentcore.
func (p *Provider) SystemPromptBlock() string {
	return "You have access to a memory store. Relevant memories will be injected before each response."
}

// extractText concatenates all text content parts from a message.
func extractText(msg agentcore.AgentMessage) string {
	var s string
	for _, p := range msg.Content {
		if p.Text != "" {
			s += p.Text
		}
	}
	return s
}
