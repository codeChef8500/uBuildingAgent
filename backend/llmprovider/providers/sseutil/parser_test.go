package sseutil

import (
	"strings"
	"testing"
)

func TestScanner_BasicText(t *testing.T) {
	raw := "data: hello\n\ndata: world\n\n"
	scanner := NewScanner(strings.NewReader(raw))

	got := []string{}
	for scanner.Scan() {
		got = append(got, scanner.Event().Data)
	}
	if len(got) != 2 {
		t.Fatalf("expected 2 events, got %d: %v", len(got), got)
	}
	if got[0] != "hello" || got[1] != "world" {
		t.Errorf("unexpected data: %v", got)
	}
}

func TestScanner_DoneToken(t *testing.T) {
	raw := "data: [DONE]\n\n"
	scanner := NewScanner(strings.NewReader(raw))
	if !scanner.Scan() {
		t.Fatal("expected to scan event")
	}
	if !scanner.Event().IsDone() {
		t.Error("expected IsDone() = true")
	}
}

func TestScanner_EventAndID(t *testing.T) {
	raw := "id: 42\nevent: result\ndata: payload\n\n"
	scanner := NewScanner(strings.NewReader(raw))
	if !scanner.Scan() {
		t.Fatal("expected to scan event")
	}
	ev := scanner.Event()
	if ev.ID != "42" {
		t.Errorf("ID: got %q, want %q", ev.ID, "42")
	}
	if ev.Type != "result" {
		t.Errorf("Type: got %q, want %q", ev.Type, "result")
	}
	if ev.Data != "payload" {
		t.Errorf("Data: got %q, want %q", ev.Data, "payload")
	}
}

func TestScanner_MultilineData(t *testing.T) {
	raw := "data: line1\ndata: line2\n\n"
	scanner := NewScanner(strings.NewReader(raw))
	if !scanner.Scan() {
		t.Fatal("expected to scan event")
	}
	got := scanner.Event().Data
	if got != "line1\nline2" {
		t.Errorf("Data: got %q, want %q", got, "line1\nline2")
	}
}

func TestScanner_CommentLines(t *testing.T) {
	raw := ": this is a comment\ndata: real\n\n"
	scanner := NewScanner(strings.NewReader(raw))
	if !scanner.Scan() {
		t.Fatal("expected to scan event")
	}
	if scanner.Event().Data != "real" {
		t.Errorf("expected 'real', got %q", scanner.Event().Data)
	}
}

func TestScanner_EmptyInput(t *testing.T) {
	scanner := NewScanner(strings.NewReader(""))
	if scanner.Scan() {
		t.Error("expected no events for empty input")
	}
	if err := scanner.Err(); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestScanner_MultipleEvents(t *testing.T) {
	raw := "data: a\n\ndata: b\n\ndata: c\n\n"
	scanner := NewScanner(strings.NewReader(raw))
	count := 0
	for scanner.Scan() {
		count++
	}
	if count != 3 {
		t.Errorf("expected 3 events, got %d", count)
	}
}
