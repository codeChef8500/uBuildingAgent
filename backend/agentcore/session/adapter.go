package session

import (
	"encoding/json"
	"fmt"
	"time"
)

// LoopWriter bridges session.SessionStorage to agentcore.SessionWriter.
// It accepts the map[string]any entries emitted by the agent loop and converts
// them into SessionEntry records before persisting.
type LoopWriter struct {
	storage   SessionStorage
	idCounter int
	sessionID string
	lastID    string
}

// NewLoopWriter wraps a SessionStorage for use as agentcore.SessionWriter.
func NewLoopWriter(storage SessionStorage, sessionID string) *LoopWriter {
	return &LoopWriter{storage: storage, sessionID: sessionID}
}

// AppendEntry implements agentcore.SessionWriter.
// entry must be a map[string]any with at least a "type" key.
func (w *LoopWriter) AppendEntry(entry interface{}) error {
	m, ok := entry.(map[string]any)
	if !ok {
		return fmt.Errorf("session LoopWriter: expected map[string]any, got %T", entry)
	}

	typStr, _ := m["type"].(string)
	entryType := EntryType(typStr)

	payload, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("session LoopWriter: marshal payload: %w", err)
	}

	w.idCounter++
	id := fmt.Sprintf("%s_%d_%d", w.sessionID, time.Now().UnixMilli(), w.idCounter)

	se := SessionEntry{
		ID:        id,
		ParentID:  w.lastID,
		Timestamp: time.Now(),
		Type:      entryType,
		Payload:   payload,
	}
	if err := w.storage.AppendEntry(se); err != nil {
		return err
	}
	w.lastID = id
	return nil
}

// Storage returns the underlying SessionStorage for direct inspection in tests.
func (w *LoopWriter) Storage() SessionStorage {
	return w.storage
}
