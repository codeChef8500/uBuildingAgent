package session

import (
	"encoding/json"
	"testing"
	"time"
)

func makeEntry(id, parentID string, typ EntryType) SessionEntry {
	payload, _ := json.Marshal(map[string]string{"data": id})
	return SessionEntry{
		ID:        id,
		ParentID:  parentID,
		Timestamp: time.Now(),
		Type:      typ,
		Payload:   payload,
	}
}

func TestInMemory_AppendAndGet(t *testing.T) {
	s := NewInMemoryStorage("s1")
	e1 := makeEntry("e1", "", EntryTypeMessage)
	e2 := makeEntry("e2", "e1", EntryTypeMessage)

	if err := s.AppendEntry(e1); err != nil {
		t.Fatal(err)
	}
	if err := s.AppendEntry(e2); err != nil {
		t.Fatal(err)
	}

	entries, err := s.GetEntries()
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
}

func TestInMemory_DuplicateID(t *testing.T) {
	s := NewInMemoryStorage("s1")
	e := makeEntry("dup", "", EntryTypeMessage)
	_ = s.AppendEntry(e)
	if err := s.AppendEntry(e); err == nil {
		t.Error("expected error for duplicate ID")
	}
}

func TestInMemory_GetPathToRoot(t *testing.T) {
	s := NewInMemoryStorage("s1")
	e1 := makeEntry("root", "", EntryTypeMessage)
	e2 := makeEntry("mid", "root", EntryTypeMessage)
	e3 := makeEntry("leaf", "mid", EntryTypeMessage)

	for _, e := range []SessionEntry{e1, e2, e3} {
		_ = s.AppendEntry(e)
	}

	path, err := s.GetPathToRoot("leaf")
	if err != nil {
		t.Fatal(err)
	}
	if len(path) != 3 {
		t.Fatalf("path length: got %d, want 3", len(path))
	}
	if path[0].ID != "root" || path[2].ID != "leaf" {
		t.Errorf("path order wrong: %v", pathIDs(path))
	}
}

func TestInMemory_Fork(t *testing.T) {
	s := NewInMemoryStorage("orig")
	e1 := makeEntry("a", "", EntryTypeMessage)
	e2 := makeEntry("b", "a", EntryTypeMessage)
	e3 := makeEntry("c", "b", EntryTypeMessage)
	for _, e := range []SessionEntry{e1, e2, e3} {
		_ = s.AppendEntry(e)
	}

	forked, err := s.Fork("b", "fork1")
	if err != nil {
		t.Fatal(err)
	}

	// Forked storage should contain e1 and e2 only
	fEntries, _ := forked.GetEntries()
	if len(fEntries) != 2 {
		t.Fatalf("fork entries: got %d, want 2", len(fEntries))
	}

	// Append to fork should not affect original
	_ = forked.AppendEntry(makeEntry("fork_only", "b", EntryTypeMessage))
	origEntries, _ := s.GetEntries()
	if len(origEntries) != 3 {
		t.Errorf("original affected by fork: now has %d entries", len(origEntries))
	}
}

func TestInMemory_Metadata(t *testing.T) {
	s := NewInMemoryStorage("meta_test")
	_ = s.AppendEntry(makeEntry("m1", "", EntryTypeMessage))
	_ = s.AppendEntry(makeEntry("m2", "m1", EntryTypeMessage))

	meta, err := s.GetMetadata()
	if err != nil {
		t.Fatal(err)
	}
	if meta.MessageCount != 2 {
		t.Errorf("MessageCount: got %d, want 2", meta.MessageCount)
	}

	meta.Title = "updated"
	_ = s.UpdateMetadata(meta)
	meta2, _ := s.GetMetadata()
	if meta2.Title != "updated" {
		t.Errorf("Title: got %q", meta2.Title)
	}
}

func pathIDs(entries []SessionEntry) []string {
	ids := make([]string, len(entries))
	for i, e := range entries {
		ids[i] = e.ID
	}
	return ids
}
