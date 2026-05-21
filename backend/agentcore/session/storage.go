package session

// SessionStorage defines the persistence contract for session entries.
// All implementations must be concurrency-safe.
type SessionStorage interface {
	// AppendEntry appends a new entry to the session.
	// The entry's ID must be unique within the storage.
	AppendEntry(entry SessionEntry) error

	// GetEntries returns all entries in insertion order.
	GetEntries() ([]SessionEntry, error)

	// GetPathToRoot returns the ordered path [root, ..., entry]
	// following the ParentID chain from the given entryID up to the root.
	// Returns an error if entryID is not found.
	GetPathToRoot(entryID string) ([]SessionEntry, error)

	// GetMetadata returns summary metadata for this session.
	GetMetadata() (SessionMetadata, error)

	// UpdateMetadata stores updated metadata (title, token counts, etc.).
	UpdateMetadata(meta SessionMetadata) error
}
