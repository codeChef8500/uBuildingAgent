//go:build integration

package providers_test

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ubuildingagent/backend/internal/envconfig"
	"github.com/ubuildingagent/backend/llmprovider"
	_ "github.com/ubuildingagent/backend/llmprovider/providers" // trigger init() → register builtins
)

// envFile resolves the path to backend/.env relative to this test file.
func envFile() string {
	_, thisFile, _, _ := runtime.Caller(0)
	// thisFile = .../backend/llmprovider/providers/e2e_test.go
	// .env is three directories up: providers/ → llmprovider/ → backend/
	return filepath.Join(filepath.Dir(thisFile), "..", "..", ".env")
}

// loadConfig reads .env and skips the test if the file is missing or incomplete.
func loadConfig(t *testing.T) (*envconfig.LLMConfig, llmprovider.Model, llmprovider.StreamOptions) {
	t.Helper()
	cfg, err := envconfig.LoadFromFile(envFile())
	if err != nil {
		t.Skipf("skipping integration test: .env not found (%v)", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Skipf("skipping integration test: incomplete .env (%v)", err)
	}
	t.Logf("LLM_TYPE=%s  MODEL=%s  BASE_URL=%s", cfg.Type, cfg.Model, cfg.BaseURL)
	return cfg, cfg.ToModel(), cfg.ToStreamOptions()
}

// collectEvents drains a StreamEvent channel and returns all events.
// The test fails if any StreamEventError is received or the timeout expires.
func collectEvents(t *testing.T, ch <-chan llmprovider.StreamEvent, timeout time.Duration) []llmprovider.StreamEvent {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var events []llmprovider.StreamEvent
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return events
			}
			if ev.Type == llmprovider.StreamEventError {
				t.Fatalf("StreamEvent error: %v", ev.Err)
			}
			events = append(events, ev)
		case <-ctx.Done():
			t.Fatalf("timeout waiting for stream events (%s)", timeout)
			return events
		}
	}
}

// reconstructText joins all TextDelta events into a single string.
func reconstructText(events []llmprovider.StreamEvent) string {
	var sb strings.Builder
	for _, ev := range events {
		if ev.Type == llmprovider.StreamEventTextDelta {
			sb.WriteString(ev.Delta)
		}
	}
	return sb.String()
}

// ── T1: Single-turn text stream ────────────────────────────────────────────

func TestE2E_SimpleStream(t *testing.T) {
	_, model, opts := loadConfig(t)

	conv := llmprovider.Context{
		Messages: []llmprovider.Message{
			{
				Role:    llmprovider.RoleUser,
				Content: []llmprovider.ContentPart{
					{Type: llmprovider.ContentTypeText, Text: "用一句话介绍你自己。"},
				},
			},
		},
	}

	ctx := context.Background()
	ch := llmprovider.Stream(ctx, model, conv, opts)
	events := collectEvents(t, ch, 30*time.Second)

	// Assertions
	text := reconstructText(events)
	if text == "" {
		t.Error("expected non-empty text response")
	}
	t.Logf("Response: %s", text)

	// Verify MessageEnd event present
	hasEnd := false
	for _, ev := range events {
		if ev.Type == llmprovider.StreamEventMessageEnd {
			hasEnd = true
			t.Logf("StopReason=%s  Usage=%+v", ev.StopReason, ev.Usage)
		}
	}
	if !hasEnd {
		t.Error("expected StreamEventMessageEnd in event stream")
	}
}

// ── T2: Multi-turn conversation ────────────────────────────────────────────

func TestE2E_MultiTurn(t *testing.T) {
	_, model, opts := loadConfig(t)

	// First turn
	conv := llmprovider.Context{
		Messages: []llmprovider.Message{
			{
				Role: llmprovider.RoleUser,
				Content: []llmprovider.ContentPart{
					{Type: llmprovider.ContentTypeText, Text: "我的名字是小明。请记住它。"},
				},
			},
		},
	}

	ctx := context.Background()
	ch := llmprovider.Stream(ctx, model, conv, opts)
	firstEvents := collectEvents(t, ch, 30*time.Second)
	firstReply := reconstructText(firstEvents)
	t.Logf("Turn 1 reply: %s", firstReply)

	// Second turn — expect model to remember the name
	conv.Messages = append(conv.Messages,
		llmprovider.Message{
			Role: llmprovider.RoleAssistant,
			Content: []llmprovider.ContentPart{
				{Type: llmprovider.ContentTypeText, Text: firstReply},
			},
		},
		llmprovider.Message{
			Role: llmprovider.RoleUser,
			Content: []llmprovider.ContentPart{
				{Type: llmprovider.ContentTypeText, Text: "我的名字是什么？"},
			},
		},
	)

	ch2 := llmprovider.Stream(ctx, model, conv, opts)
	secondEvents := collectEvents(t, ch2, 30*time.Second)
	secondReply := reconstructText(secondEvents)
	t.Logf("Turn 2 reply: %s", secondReply)

	if secondReply == "" {
		t.Error("expected non-empty second-turn response")
	}
	if !strings.Contains(secondReply, "小明") {
		t.Errorf("expected model to recall name '小明', got: %s", secondReply)
	}
}

// ── T3: Tool call ──────────────────────────────────────────────────────────

func TestE2E_ToolCall(t *testing.T) {
	_, model, opts := loadConfig(t)

	// Define a simple tool
	conv := llmprovider.Context{
		Messages: []llmprovider.Message{
			{
				Role: llmprovider.RoleUser,
				Content: []llmprovider.ContentPart{
					{Type: llmprovider.ContentTypeText, Text: "现在是几点？请调用工具查询。"},
				},
			},
		},
		Tools: []llmprovider.Tool{
			{
				Name:        "get_current_time",
				Description: "返回当前的本地时间（ISO 8601格式）",
				Parameters:  json.RawMessage(`{"type":"object","properties":{},"required":[]}`),
			},
		},
	}

	ctx := context.Background()
	ch := llmprovider.Stream(ctx, model, conv, opts)
	events := collectEvents(t, ch, 30*time.Second)

	// Find tool call events
	var toolStarts, toolEnds []llmprovider.StreamEvent
	for _, ev := range events {
		switch ev.Type {
		case llmprovider.StreamEventToolCallStart:
			toolStarts = append(toolStarts, ev)
			t.Logf("ToolCallStart: id=%s name=%s", ev.ToolCall.ID, ev.ToolCall.Name)
		case llmprovider.StreamEventToolCallEnd:
			toolEnds = append(toolEnds, ev)
			t.Logf("ToolCallEnd: name=%s args=%s", ev.ToolCall.Name, ev.ToolCall.ArgsDelta)
		}
	}

	if len(toolStarts) == 0 {
		t.Skip("model did not emit a tool call (may not support function calling on this endpoint)")
	}
	if len(toolEnds) == 0 {
		t.Error("expected ToolCallEnd event after ToolCallStart")
	}
	if toolStarts[0].ToolCall.Name != "get_current_time" {
		t.Errorf("expected tool name %q, got %q", "get_current_time", toolStarts[0].ToolCall.Name)
	}

	// Second turn: provide tool result and get final answer
	toolCallID := toolStarts[0].ToolCall.ID
	now := fmt.Sprintf(`{"time":"%s"}`, time.Now().Format(time.RFC3339))

	conv.Messages = append(conv.Messages,
		llmprovider.Message{
			Role: llmprovider.RoleAssistant,
			ToolCalls: []llmprovider.ToolCall{
				{
					ID:        toolCallID,
					Name:      "get_current_time",
					Arguments: json.RawMessage(`{}`),
				},
			},
		},
		llmprovider.Message{
			Role: llmprovider.RoleTool,
			Content: []llmprovider.ContentPart{
				{
					Type:       llmprovider.ContentTypeToolResult,
					ToolCallID: toolCallID,
					ToolResult: now,
				},
			},
		},
	)

	ch2 := llmprovider.Stream(ctx, model, conv, opts)
	secondEvents := collectEvents(t, ch2, 30*time.Second)
	finalReply := reconstructText(secondEvents)
	t.Logf("Final reply after tool result: %s", finalReply)

	if finalReply == "" {
		t.Error("expected non-empty reply after tool result injection")
	}
}
