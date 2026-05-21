package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// JSONLStorage is a file-backed SessionStorage.
// Entries are stored one JSON object per line (JSONL format).
// An in-memory index is kept for fast ID lookup.
//
// The file is append-only; reads reload from disk lazily or can be cached.
type JSONLStorage struct {
	mu       sync.Mutex
	path     string
	byID     map[string]int // id → line index (0-based)
	entries  []SessionEntry // in-memory cache
	meta     SessionMetadata
	metaPath string // optional sidecar file for metadata
}

// NewJSONLStorage opens or creates a JSONL session file at path.
// If the file exists, all entries are loaded into memory.
func NewJSONLStorage(path string, sessionID string) (*JSONLStorage, error) {
	s := &JSONLStorage{
		path:     path,
		metaPath: path + ".meta.json",
		byID:     make(map[string]int),
		meta: SessionMetadata{
			ID:        sessionID,
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		},
	}
	if err := s.loadFromDisk(); err != nil {
		return nil, err
	}
	_ = s.loadMeta()
	return s, nil
}

func (s *JSONLStorage) loadFromDisk() error {
	f, err := os.Open(s.path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("session jsonl: open %q: %w", s.path, err)
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 1024*1024), 8*1024*1024)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}
		var entry SessionEntry
		if err := json.Unmarshal(line, &entry); err != nil {
			return fmt.Errorf("session jsonl: parse line: %w", err)
		}
		s.byID[entry.ID] = len(s.entries)
		s.entries = append(s.entries, entry)
	}
	return scanner.Err()
}

func (s *JSONLStorage) loadMeta() error {
	data, err := os.ReadFile(s.metaPath)
	if err != nil {
		return err
	}
	return json.Unmarshal(data, &s.meta)
}

func (s *JSONLStorage) saveMeta() error {
	data, err := json.Marshal(s.meta)
	if err != nil {
		return err
	}
	return os.WriteFile(s.metaPath, data, 0644)
}

// AppendEntry appends an entry to both the in-memory cache and the JSONL file.
func (s *JSONLStorage) AppendEntry(entry SessionEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.byID[entry.ID]; exists {
		return fmt.Errorf("session: duplicate entry ID %q", entry.ID)
	}

	data, err := json.Marshal(entry)
	if err != nil {
		return fmt.Errorf("session jsonl: marshal: %w", err)
	}

	f, err := os.OpenFile(s.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("session jsonl: open for append: %w", err)
	}
	_, writeErr := fmt.Fprintf(f, "%s\n", data)
	closeErr := f.Close()
	if writeErr != nil {
		return fmt.Errorf("session jsonl: write: %w", writeErr)
	}
	if closeErr != nil {
		return fmt.Errorf("session jsonl: close: %w", closeErr)
	}

	s.byID[entry.ID] = len(s.entries)
	s.entries = append(s.entries, entry)
	s.meta.UpdatedAt = time.Now()
	if entry.Type == EntryTypeMessage {
		s.meta.MessageCount++
	}
	_ = s.saveMeta()
	return nil
}

// GetEntries returns all entries in order.
func (s *JSONLStorage) GetEntries() ([]SessionEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]SessionEntry, len(s.entries))
	copy(out, s.entries)
	return out, nil
}

// GetPathToRoot walks the parent chain as in InMemoryStorage.
func (s *JSONLStorage) GetPathToRoot(entryID string) ([]SessionEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	idx, ok := s.byID[entryID]
	if !ok {
		return nil, fmt.Errorf("session: entry %q not found", entryID)
	}
	idToEntry := make(map[string]SessionEntry, len(s.entries))
	for _, e := range s.entries {
		idToEntry[e.ID] = e
	}
	cur := s.entries[idx]
	var path []SessionEntry
	for {
		path = append([]SessionEntry{cur}, path...)
		if cur.ParentID == "" {
			break
		}
		parent, ok := idToEntry[cur.ParentID]
		if !ok {
			return nil, fmt.Errorf("session: parent %q not found", cur.ParentID)
		}
		cur = parent
	}
	return path, nil
}

// GetMetadata returns session metadata.
func (s *JSONLStorage) GetMetadata() (SessionMetadata, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.meta, nil
}

// UpdateMetadata persists updated metadata.
func (s *JSONLStorage) UpdateMetadata(meta SessionMetadata) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.meta = meta
	return s.saveMeta()
}
