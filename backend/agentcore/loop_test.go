package agentcore

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/ubuildingagent/backend/llmprovider"
)

// mockProvider is a minimal ApiProvider for loop testing.
type mockProvider struct {
	responses []mockTurn
	callIdx   int
}

// mockTurn describes one LLM response turn.
type mockTurn struct {
	text      string
	toolCalls []llmprovider.ToolCall
}

func (m *mockProvider) ApiType() llmprovider.ApiType {
	return llmprovider.ApiOpenAICompletions
}

func (m *mockProvider) Stream(
	ctx context.Context,
	model llmprovider.Model,
	conv llmprovider.Context,
	opts llmprovider.StreamOptions,
) <-chan llmprovider.StreamEvent {
	return m.emit()
}

func (m *mockProvider) StreamSimple(
	ctx context.Context,
	model llmprovider.Model,
	conv llmprovider.Context,
	opts llmprovider.SimpleStreamOptions,
) <-chan llmprovider.StreamEvent {
	return m.emit()
}

func (m *mockProvider) emit() <-chan llmprovider.StreamEvent {
	ch := make(chan llmprovider.StreamEvent, 16)
	idx := m.callIdx
	if idx >= len(m.responses) {
		idx = len(m.responses) - 1
	}
	m.callIdx++
	turn := m.responses[idx]
	go func() {
		defer close(ch)
		if turn.text != "" {
			ch <- llmprovider.StreamEvent{Type: llmprovider.StreamEventTextDelta, Delta: turn.text}
		}
		for i, tc := range turn.toolCalls {
			ch <- llmprovider.StreamEvent{
				Type:     llmprovider.StreamEventToolCallStart,
				ToolCall: &llmprovider.ToolCallDelta{Index: i, ID: tc.ID, Name: tc.Name},
			}
			ch <- llmprovider.StreamEvent{
				Type:     llmprovider.StreamEventToolCallEnd,
				ToolCall: &llmprovider.ToolCallDelta{Index: i, ID: tc.ID, Name: tc.Name, ArgsDelta: string(tc.Arguments)},
			}
		}
		ch <- llmprovider.StreamEvent{
			Type:  llmprovider.StreamEventMessageEnd,
			Usage: &llmprovider.Usage{InputTokens: 10, OutputTokens: 20},
		}
	}()
	return ch
}

// setupMockRegistry registers the mock provider and returns a cleanup function.
func setupMockRegistry(mp *mockProvider) func() {
	llmprovider.Register(mp, "test-mock")
	return func() { llmprovider.Unregister("test-mock") }
}

// ── Tests ─────────────────────────────────────────────────────────────────

func TestLoop_SimpleText(t *testing.T) {
	mp := &mockProvider{responses: []mockTurn{{text: "Hello from LLM"}}}
	cleanup := setupMockRegistry(mp)
	defer cleanup()

	cfg := AgentLoopConfig{
		Model: llmprovider.Model{
			ID:  "mock-model",
			Api: llmprovider.ApiOpenAICompletions,
		},
	}
	conv := AgentContext{
		Messages: []AgentMessage{
			{Role: llmprovider.RoleUser, Content: []llmprovider.ContentPart{{Type: llmprovider.ContentTypeText, Text: "Hi"}}},
		},
	}

	ch := RunAgentLoop(context.Background(), cfg, conv)
	var textParts []string
	for ev := range ch {
		if ev.Type == AgentEventTextDelta {
			textParts = append(textParts, ev.Delta)
		}
		if ev.Type == AgentEventError {
			t.Fatalf("unexpected error: %v", ev.Err)
		}
	}
	reply := strings.Join(textParts, "")
	if reply != "Hello from LLM" {
		t.Errorf("reply: got %q", reply)
	}
}

func TestLoop_SingleToolCall(t *testing.T) {
	tc := llmprovider.ToolCall{
		ID:        "call_1",
		Name:      "get_time",
		Arguments: json.RawMessage(`{}`),
	}
	// Turn 1: LLM calls tool; Turn 2: LLM sends final answer
	mp := &mockProvider{responses: []mockTurn{
		{toolCalls: []llmprovider.ToolCall{tc}},
		{text: "The time is noon"},
	}}
	cleanup := setupMockRegistry(mp)
	defer cleanup()

	toolCalled := false
	cfg := AgentLoopConfig{
		Model: llmprovider.Model{ID: "mock", Api: llmprovider.ApiOpenAICompletions},
	}
	conv := AgentContext{
		Messages: []AgentMessage{
			{Role: llmprovider.RoleUser, Content: []llmprovider.ContentPart{{Type: llmprovider.ContentTypeText, Text: "What time is it?"}}},
		},
		Tools: []AgentTool{
			{
				Name: "get_time",
				Execute: func(ctx *ToolExecContext) AgentToolResult {
					toolCalled = true
					return AgentToolResult{Content: "12:00"}
				},
			},
		},
	}

	var events []AgentEventType
	ch := RunAgentLoop(context.Background(), cfg, conv)
	for ev := range ch {
		events = append(events, ev.Type)
		if ev.Type == AgentEventError {
			t.Fatalf("error: %v", ev.Err)
		}
	}

	if !toolCalled {
		t.Error("expected tool to be called")
	}
	// Must have seen tool_start and tool_end
	has := func(et AgentEventType) bool {
		for _, e := range events {
			if e == et {
				return true
			}
		}
		return false
	}
	if !has(AgentEventToolStart) {
		t.Error("expected AgentEventToolStart")
	}
	if !has(AgentEventToolEnd) {
		t.Error("expected AgentEventToolEnd")
	}
}

func TestLoop_BudgetExceeded(t *testing.T) {
	// Each turn returns a tool call so the loop would run forever without budget
	tc := llmprovider.ToolCall{ID: "c1", Name: "loop_tool", Arguments: json.RawMessage(`{}`)}
	mp := &mockProvider{responses: []mockTurn{
		{toolCalls: []llmprovider.ToolCall{tc}},
		{toolCalls: []llmprovider.ToolCall{tc}},
		{toolCalls: []llmprovider.ToolCall{tc}},
	}}
	cleanup := setupMockRegistry(mp)
	defer cleanup()

	cfg := AgentLoopConfig{
		Model:  llmprovider.Model{ID: "mock", Api: llmprovider.ApiOpenAICompletions},
		Budget: NewIterationBudget(2, 0),
	}
	conv := AgentContext{
		Messages: []AgentMessage{
			{Role: llmprovider.RoleUser, Content: []llmprovider.ContentPart{{Type: llmprovider.ContentTypeText, Text: "go"}}},
		},
		Tools: []AgentTool{
			{Name: "loop_tool", Execute: func(ctx *ToolExecContext) AgentToolResult {
				return AgentToolResult{Content: "ok"}
			}},
		},
	}

	var gotError bool
	for ev := range RunAgentLoop(context.Background(), cfg, conv) {
		if ev.Type == AgentEventError {
			gotError = true
		}
	}
	if !gotError {
		t.Error("expected budget exceeded error")
	}
}

func TestLoop_ShouldStopAfterTurn(t *testing.T) {
	mp := &mockProvider{responses: []mockTurn{
		{text: "turn1"},
		{text: "turn2"},
	}}
	cleanup := setupMockRegistry(mp)
	defer cleanup()

	turnCount := 0
	cfg := AgentLoopConfig{
		Model: llmprovider.Model{ID: "mock", Api: llmprovider.ApiOpenAICompletions},
		ShouldStopAfterTurn: func(ctx *ShouldStopContext) bool {
			turnCount++
			return true // stop immediately
		},
	}
	conv := AgentContext{
		Messages: []AgentMessage{
			{Role: llmprovider.RoleUser, Content: []llmprovider.ContentPart{{Type: llmprovider.ContentTypeText, Text: "hi"}}},
		},
	}

	for ev := range RunAgentLoop(context.Background(), cfg, conv) {
		if ev.Type == AgentEventError {
			t.Fatalf("error: %v", ev.Err)
		}
	}
	if turnCount != 1 {
		t.Errorf("expected 1 stop check, got %d", turnCount)
	}
}

func TestLoop_ParallelTools(t *testing.T) {
	calls := []llmprovider.ToolCall{
		{ID: "c1", Name: "tool_a", Arguments: json.RawMessage(`{}`)},
		{ID: "c2", Name: "tool_b", Arguments: json.RawMessage(`{}`)},
	}
	mp := &mockProvider{responses: []mockTurn{
		{toolCalls: calls},
		{text: "done"},
	}}
	cleanup := setupMockRegistry(mp)
	defer cleanup()

	executed := map[string]bool{}
	var mu = &struct{ sync.Mutex }{}
	cfg := AgentLoopConfig{
		Model:         llmprovider.Model{ID: "mock", Api: llmprovider.ApiOpenAICompletions},
		ToolExecution: ToolExecutionParallel,
	}
	conv := AgentContext{
		Messages: []AgentMessage{
			{Role: llmprovider.RoleUser, Content: []llmprovider.ContentPart{{Type: llmprovider.ContentTypeText, Text: "parallel"}}},
		},
		Tools: []AgentTool{
			{Name: "tool_a", Execute: func(ctx *ToolExecContext) AgentToolResult {
				mu.Lock()
				executed["a"] = true
				mu.Unlock()
				return AgentToolResult{Content: "a_result"}
			}},
			{Name: "tool_b", Execute: func(ctx *ToolExecContext) AgentToolResult {
				mu.Lock()
				executed["b"] = true
				mu.Unlock()
				return AgentToolResult{Content: "b_result"}
			}},
		},
	}

	for ev := range RunAgentLoop(context.Background(), cfg, conv) {
		if ev.Type == AgentEventError {
			t.Fatalf("error: %v", ev.Err)
		}
	}
	if !executed["a"] || !executed["b"] {
		t.Errorf("not all tools executed: %v", executed)
	}
}
