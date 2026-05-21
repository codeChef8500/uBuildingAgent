package session

import (
	"fmt"
	"sync"
	"time"
)

// InMemoryStorage is a concurrency-safe in-process SessionStorage.
// It supports tree-structured forking: multiple InMemoryStorage instances
// can share a base slice of entries and extend it independently.
type InMemoryStorage struct {
	mu      sync.RWMutex
	entries []SessionEntry
	byID    map[string]int // id → index in entries
	meta    SessionMetadata
}

// NewInMemoryStorage creates an empty in-memory storage.
func NewInMemoryStorage(sessionID string) *InMemoryStorage {
	s := &InMemoryStorage{
		byID: make(map[string]int),
		meta: SessionMetadata{
			ID:        sessionID,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
	}
	return s
}

// forkFrom creates a new InMemoryStorage pre-seeded with the given entries.
// The new storage is fully independent (deep copy).
func forkFrom(entries []SessionEntry, newID string) *InMemoryStorage {
	s := NewInMemoryStorage(newID)
	for i, e := range entries {
		s.entries = append(s.entries, e)
		s.byID[e.ID] = i
	}
	return s
}

// AppendEntry appends a new entry.
func (s *InMemoryStorage) AppendEntry(entry SessionEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.byID[entry.ID]; exists {
		return fmt.Errorf("session: duplicate entry ID %q", entry.ID)
	}
	s.byID[entry.ID] = len(s.entries)
	s.entries = append(s.entries, entry)
	s.meta.UpdatedAt = time.Now()
	if entry.Type == EntryTypeMessage {
		s.meta.MessageCount++
	}
	return nil
}

// GetEntries returns all entries in insertion order.
func (s *InMemoryStorage) GetEntries() ([]SessionEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]SessionEntry, len(s.entries))
	copy(out, s.entries)
	return out, nil
}

// GetPathToRoot walks parent chain from entryID to the root.
// Returns [root, ..., entryID] (inclusive on both ends).
func (s *InMemoryStorage) GetPathToRoot(entryID string) ([]SessionEntry, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	idx, ok := s.byID[entryID]
	if !ok {
		return nil, fmt.Errorf("session: entry %q not found", entryID)
	}

	// Build a set for fast lookup
	idToEntry := make(map[string]SessionEntry, len(s.entries))
	for _, e := range s.entries {
		idToEntry[e.ID] = e
	}

	// Walk parent chain
	path := []SessionEntry{}
	cur := s.entries[idx]
	for {
		path = append([]SessionEntry{cur}, path...)
		if cur.ParentID == "" {
			break
		}
		parent, ok := idToEntry[cur.ParentID]
		if !ok {
			return nil, fmt.Errorf("session: parent %q of entry %q not found", cur.ParentID, cur.ID)
		}
		cur = parent
	}
	return path, nil
}

// GetMetadata returns a copy of the metadata.
func (s *InMemoryStorage) GetMetadata() (SessionMetadata, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.meta, nil
}

// UpdateMetadata replaces the metadata.
func (s *InMemoryStorage) UpdateMetadata(meta SessionMetadata) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.meta = meta
	return nil
}

// Fork creates a new independent storage seeded with entries up to (and
// including) the entry with forkEntryID.  If forkEntryID is empty, the fork
// starts from the beginning (full copy).
func (s *InMemoryStorage) Fork(forkEntryID string, newSessionID string) (*InMemoryStorage, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if forkEntryID == "" {
		entries := make([]SessionEntry, len(s.entries))
		copy(entries, s.entries)
		return forkFrom(entries, newSessionID), nil
	}

	idx, ok := s.byID[forkEntryID]
	if !ok {
		return nil, fmt.Errorf("session: fork point %q not found", forkEntryID)
	}
	entries := make([]SessionEntry, idx+1)
	copy(entries, s.entries[:idx+1])
	return forkFrom(entries, newSessionID), nil
}
