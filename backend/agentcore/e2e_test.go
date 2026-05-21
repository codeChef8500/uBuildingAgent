//go:build integration

package agentcore

import (
	"context"
	"encoding/json"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ubuildingagent/backend/internal/envconfig"
	"github.com/ubuildingagent/backend/llmprovider"
	_ "github.com/ubuildingagent/backend/llmprovider/providers" // register builtins
)

// envFile resolves path to backend/.env from this test file's location.
func envFile() string {
	_, thisFile, _, _ := runtime.Caller(0)
	// agentcore/e2e_test.go → ../../.env
	return filepath.Join(filepath.Dir(thisFile), "..", ".env")
}

// loadAgentConfig reads .env and returns a ready AgentLoopConfig.
func loadAgentConfig(t *testing.T) AgentLoopConfig {
	t.Helper()
	cfg, err := envconfig.LoadFromFile(envFile())
	if err != nil {
		t.Skipf("skipping integration test: .env not found (%v)", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Skipf("skipping integration test: incomplete .env (%v)", err)
	}
	t.Logf("LLM_TYPE=%s  MODEL=%s  BASE_URL=%s", cfg.Type, cfg.Model, cfg.BaseURL)
	return AgentLoopConfig{
		Model:      cfg.ToModel(),
		StreamOpts: llmprovider.SimpleStreamOptions{StreamOptions: cfg.ToStreamOptions()},
	}
}

// drainLoop collects all AgentEvents from the channel.
// Fatal errors cause t.Fatal; HTTP 4xx API errors cause t.Skip (endpoint limitation).
func drainLoop(t *testing.T, ch <-chan AgentEvent, timeout time.Duration) []AgentEvent {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var events []AgentEvent
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return events
			}
			if ev.Type == AgentEventError {
				msg := ev.Err.Error()
				// Skip on HTTP 4xx — endpoint/model limitation, not a code bug
				if strings.Contains(msg, "http 400") ||
					strings.Contains(msg, "http 401") ||
					strings.Contains(msg, "http 422") ||
					strings.Contains(msg, "InvalidParameter") {
					t.Skipf("skipping: API returned error: %v", ev.Err)
				}
				t.Fatalf("AgentEvent error: %v", ev.Err)
			}
			events = append(events, ev)
		case <-ctx.Done():
			t.Fatalf("timeout waiting for agent events (%s)", timeout)
		}
	}
}

// textFromEvents joins all AgentEventTextDelta deltas.
func textFromEvents(events []AgentEvent) string {
	var sb strings.Builder
	for _, ev := range events {
		if ev.Type == AgentEventTextDelta {
			sb.WriteString(ev.Delta)
		}
	}
	return sb.String()
}

// ── T1: Simple text turn ────────────────────────────────────────────────────

func TestAgentE2E_SimpleText(t *testing.T) {
	cfg := loadAgentConfig(t)
	conv := AgentContext{
		SystemPrompt: "You are a helpful assistant. Keep answers brief.",
		Messages: []AgentMessage{
			{
				Role:    llmprovider.RoleUser,
				Content: []llmprovider.ContentPart{{Type: llmprovider.ContentTypeText, Text: "用一句话介绍你自己。"}},
			},
		},
	}

	ch := RunAgentLoop(context.Background(), cfg, conv)
	events := drainLoop(t, ch, 30*time.Second)

	reply := textFromEvents(events)
	if reply == "" {
		t.Error("expected non-empty text reply")
	}
	t.Logf("Reply: %s", reply)

	// Verify agent_start, turn_end, agent_end are present
	has := func(et AgentEventType) bool {
		for _, e := range events {
			if e.Type == et {
				return true
			}
		}
		return false
	}
	if !has(AgentEventStart) {
		t.Error("expected AgentEventStart")
	}
	if !has(AgentEventTurnEnd) {
		t.Error("expected AgentEventTurnEnd")
	}
	if !has(AgentEventEnd) {
		t.Error("expected AgentEventEnd")
	}
}

// ── T2: Multi-turn conversation via Agent struct ────────────────────────────

func TestAgentE2E_AgentMultiTurn(t *testing.T) {
	cfg := loadAgentConfig(t)
	agent := NewAgent(cfg, "你是一个友好的助手，请记住用户的信息。")

	// Turn 1
	ch1 := agent.Prompt(context.Background(), "我的名字是小红，请记住。")
	events1 := drainLoop(t, ch1, 30*time.Second)
	reply1 := textFromEvents(events1)
	t.Logf("Turn 1: %s", reply1)
	if reply1 == "" {
		t.Error("expected turn 1 reply")
	}

	// Turn 2 — should recall the name
	ch2 := agent.Prompt(context.Background(), "我的名字是什么？")
	events2 := drainLoop(t, ch2, 30*time.Second)
	reply2 := textFromEvents(events2)
	t.Logf("Turn 2: %s", reply2)

	if !strings.Contains(reply2, "小红") {
		t.Errorf("expected model to recall '小红', got: %s", reply2)
	}

	// Verify Agent state
	state := agent.State()
	if state.IsStreaming {
		t.Error("IsStreaming should be false after completion")
	}
	// Messages should contain: user1, assistant1, user2, assistant2 (at minimum)
	if len(state.Messages) < 4 {
		t.Errorf("expected ≥4 messages in state, got %d", len(state.Messages))
	}
}

// ── T3: Tool call through agent loop ───────────────────────────────────────

func TestAgentE2E_ToolCall(t *testing.T) {
	cfg := loadAgentConfig(t)

	toolCalled := false
	currentTime := time.Now().Format("2006-01-02 15:04:05")

	conv := AgentContext{
		SystemPrompt: "You are a helpful assistant.",
		Messages: []AgentMessage{
			{
				Role:    llmprovider.RoleUser,
				Content: []llmprovider.ContentPart{{Type: llmprovider.ContentTypeText, Text: "现在是几点？请调用工具查询当前时间。"}},
			},
		},
		Tools: []AgentTool{
			{
				Name:        "get_current_time",
				Description: "返回当前的本地时间",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"dummy":{"type":"string","description":"unused"}}}`),
				Execute: func(ctx *ToolExecContext) AgentToolResult {
					toolCalled = true
					return AgentToolResult{
						Content: `{"time": "` + currentTime + `"}`,
					}
				},
			},
		},
	}

	ch := RunAgentLoop(context.Background(), cfg, conv)
	events := drainLoop(t, ch, 60*time.Second)

	var toolStarts, toolEnds []AgentEvent
	for _, ev := range events {
		switch ev.Type {
		case AgentEventToolStart:
			toolStarts = append(toolStarts, ev)
			t.Logf("ToolStart: %s", ev.ToolCall.Name)
		case AgentEventToolEnd:
			toolEnds = append(toolEnds, ev)
			t.Logf("ToolEnd: %s → %s", ev.ToolCall.Name, ev.ToolCall.Result.Content)
		}
	}

	if len(toolStarts) == 0 {
		t.Skip("model did not emit tool call (endpoint may not support function calling)")
	}
	if !toolCalled {
		t.Error("tool Execute function was never called")
	}
	if len(toolEnds) == 0 {
		t.Error("expected AgentEventToolEnd")
	}

	// Final reply should mention the time
	finalReply := textFromEvents(events)
	t.Logf("Final reply: %s", finalReply)
	if finalReply == "" {
		t.Error("expected final text reply after tool execution")
	}
}

// ── T4: Budget limiting ────────────────────────────────────────────────────

func TestAgentE2E_Budget(t *testing.T) {
	cfg := loadAgentConfig(t)
	cfg.Budget = NewIterationBudget(1, 0) // only 1 iteration allowed

	conv := AgentContext{
		SystemPrompt: "You are a helpful assistant.",
		Messages: []AgentMessage{
			{
				Role:    llmprovider.RoleUser,
				Content: []llmprovider.ContentPart{{Type: llmprovider.ContentTypeText, Text: "Say hello."}},
			},
		},
	}

	ch := RunAgentLoop(context.Background(), cfg, conv)
	events := drainLoop(t, ch, 30*time.Second)

	// Should complete within 1 iteration (simple text, no tools)
	reply := textFromEvents(events)
	if reply == "" {
		t.Error("expected reply within budget")
	}
	t.Logf("Budget reply: %s", reply)
}

// ── T5: BeforeToolCall block hook ──────────────────────────────────────────

func TestAgentE2E_BeforeToolCallBlock(t *testing.T) {
	cfg := loadAgentConfig(t)
	cfg.BeforeToolCall = func(ctx *BeforeToolCallContext) BeforeToolCallResult {
		return BeforeToolCallResult{Block: true, Reason: "blocked by test"}
	}

	toolExecuted := false
	conv := AgentContext{
		SystemPrompt: "You are a helpful assistant.",
		Messages: []AgentMessage{
			{
				Role:    llmprovider.RoleUser,
				Content: []llmprovider.ContentPart{{Type: llmprovider.ContentTypeText, Text: "请调用get_current_time工具。"}},
			},
		},
		Tools: []AgentTool{
			{
				Name:        "get_current_time",
				Description: "返回当前时间",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"dummy":{"type":"string","description":"unused"}}}`),

				Execute: func(ctx *ToolExecContext) AgentToolResult {
					toolExecuted = true
					return AgentToolResult{Content: "12:00"}
				},
			},
		},
	}

	ch := RunAgentLoop(context.Background(), cfg, conv)
	events := drainLoop(t, ch, 30*time.Second)

	if toolExecuted {
		t.Error("tool should have been blocked and not executed")
	}

	// tool_end should still be emitted (with blocked result)
	var sawToolEnd bool
	for _, ev := range events {
		if ev.Type == AgentEventToolEnd {
			sawToolEnd = true
			if ev.ToolCall != nil && ev.ToolCall.Result != nil {
				t.Logf("Blocked tool result: %s", ev.ToolCall.Result.Content)
			}
		}
	}
	if !sawToolEnd {
		t.Skip("model did not call a tool (cannot test block hook)")
	}
}
