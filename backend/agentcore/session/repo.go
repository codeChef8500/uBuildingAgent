package session

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// SessionRepo manages multiple sessions stored as JSONL files under a base directory.
type SessionRepo struct {
	mu      sync.Mutex
	baseDir string
}

// NewSessionRepo creates a SessionRepo backed by baseDir.
// The directory is created if it does not exist.
func NewSessionRepo(baseDir string) (*SessionRepo, error) {
	if err := os.MkdirAll(baseDir, 0755); err != nil {
		return nil, fmt.Errorf("session repo: mkdir %q: %w", baseDir, err)
	}
	return &SessionRepo{baseDir: baseDir}, nil
}

// sessionPath returns the JSONL file path for a session ID.
func (r *SessionRepo) sessionPath(id string) string {
	return filepath.Join(r.baseDir, id+".jsonl")
}

// Create creates a new empty session and returns its storage.
func (r *SessionRepo) Create(id string) (*JSONLStorage, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	path := r.sessionPath(id)
	if _, err := os.Stat(path); err == nil {
		return nil, fmt.Errorf("session repo: session %q already exists", id)
	}
	return NewJSONLStorage(path, id)
}

// Open opens an existing session.
func (r *SessionRepo) Open(id string) (*JSONLStorage, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	path := r.sessionPath(id)
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, fmt.Errorf("session repo: session %q not found", id)
	}
	return NewJSONLStorage(path, id)
}

// List returns metadata for all sessions in the repo.
func (r *SessionRepo) List() ([]SessionMetadata, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	entries, err := os.ReadDir(r.baseDir)
	if err != nil {
		return nil, fmt.Errorf("session repo: readdir: %w", err)
	}

	var result []SessionMetadata
	for _, de := range entries {
		if de.IsDir() || !strings.HasSuffix(de.Name(), ".jsonl") {
			continue
		}
		id := strings.TrimSuffix(de.Name(), ".jsonl")
		path := r.sessionPath(id)
		s, err := NewJSONLStorage(path, id)
		if err != nil {
			continue
		}
		meta, _ := s.GetMetadata()
		result = append(result, meta)
	}
	return result, nil
}

// Delete removes a session and its metadata sidecar.
func (r *SessionRepo) Delete(id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	path := r.sessionPath(id)
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("session repo: delete %q: %w", id, err)
	}
	_ = os.Remove(path + ".meta.json")
	return nil
}

// Fork creates a new session by copying entries from source up to forkEntryID.
// forkEntryID="" copies the entire source session.
func (r *SessionRepo) Fork(sourceID string, forkEntryID string) (*JSONLStorage, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	srcPath := r.sessionPath(sourceID)
	src, err := NewJSONLStorage(srcPath, sourceID)
	if err != nil {
		return nil, fmt.Errorf("session repo: fork open source %q: %w", sourceID, err)
	}

	newID := fmt.Sprintf("%s_fork_%d", sourceID, time.Now().UnixMilli())
	newPath := r.sessionPath(newID)

	// Determine entries to copy
	var entries []SessionEntry
	if forkEntryID == "" {
		entries, err = src.GetEntries()
	} else {
		entries, err = src.GetPathToRoot(forkEntryID)
	}
	if err != nil {
		return nil, err
	}

	// Create new storage and replay entries
	dst, err := NewJSONLStorage(newPath, newID)
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if err := dst.AppendEntry(e); err != nil {
			return nil, fmt.Errorf("session repo: fork replay: %w", err)
		}
	}
	return dst, nil
}
