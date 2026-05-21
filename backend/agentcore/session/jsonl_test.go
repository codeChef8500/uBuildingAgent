package session

import (
	"os"
	"path/filepath"
	"testing"
)

func TestJSONL_WriteAndReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test.jsonl")

	s, err := NewJSONLStorage(path, "sess1")
	if err != nil {
		t.Fatal(err)
	}

	e1 := makeEntry("j1", "", EntryTypeMessage)
	e2 := makeEntry("j2", "j1", EntryTypeToolResult)
	_ = s.AppendEntry(e1)
	_ = s.AppendEntry(e2)

	// Reopen and verify
	s2, err := NewJSONLStorage(path, "sess1")
	if err != nil {
		t.Fatal(err)
	}
	entries, _ := s2.GetEntries()
	if len(entries) != 2 {
		t.Fatalf("after reopen: got %d entries, want 2", len(entries))
	}
	if entries[0].ID != "j1" || entries[1].ID != "j2" {
		t.Errorf("wrong IDs: %v", pathIDs(entries))
	}
}

func TestJSONL_GetPathToRoot(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "path.jsonl")
	s, _ := NewJSONLStorage(path, "s")

	_ = s.AppendEntry(makeEntry("r", "", EntryTypeMessage))
	_ = s.AppendEntry(makeEntry("m", "r", EntryTypeMessage))
	_ = s.AppendEntry(makeEntry("l", "m", EntryTypeMessage))

	path2, err := s.GetPathToRoot("l")
	if err != nil {
		t.Fatal(err)
	}
	if len(path2) != 3 || path2[0].ID != "r" || path2[2].ID != "l" {
		t.Errorf("path wrong: %v", pathIDs(path2))
	}
}

func TestRepo_CreateOpenDelete(t *testing.T) {
	dir := t.TempDir()
	repo, err := NewSessionRepo(dir)
	if err != nil {
		t.Fatal(err)
	}

	sess, err := repo.Create("mysession")
	if err != nil {
		t.Fatal(err)
	}
	_ = sess.AppendEntry(makeEntry("e1", "", EntryTypeMessage))

	// List
	list, err := repo.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 session in list, got %d", len(list))
	}

	// Open
	s2, err := repo.Open("mysession")
	if err != nil {
		t.Fatal(err)
	}
	entries, _ := s2.GetEntries()
	if len(entries) != 1 {
		t.Errorf("opened session: expected 1 entry, got %d", len(entries))
	}

	// Delete
	if err := repo.Delete("mysession"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "mysession.jsonl")); !os.IsNotExist(err) {
		t.Error("expected file to be deleted")
	}
}

func TestRepo_Fork(t *testing.T) {
	dir := t.TempDir()
	repo, _ := NewSessionRepo(dir)

	src, _ := repo.Create("source")
	_ = src.AppendEntry(makeEntry("x", "", EntryTypeMessage))
	_ = src.AppendEntry(makeEntry("y", "x", EntryTypeMessage))
	_ = src.AppendEntry(makeEntry("z", "y", EntryTypeMessage))

	forked, err := repo.Fork("source", "y") // fork at y (includes x,y only)
	if err != nil {
		t.Fatal(err)
	}
	entries, _ := forked.GetEntries()
	if len(entries) != 2 {
		t.Fatalf("forked entries: got %d, want 2", len(entries))
	}
}
