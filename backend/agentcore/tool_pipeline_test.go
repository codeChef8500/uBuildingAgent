package agentcore

// tool_pipeline_test.go — unit tests for the tool pipeline hooks added in
// S3/S4/S7 (ValidateInput, CheckPermission, DynamicDescription, ContextModifier).
//
// These tests use the existing mockProvider / setupMockRegistry helpers from
// loop_test.go (same package) so they run without any network call and do not
// need the "integration" build tag.
//
// Test coverage:
//   T1 - ValidateInput blocks invalid args before Execute
//   T2 - CheckPermission=Deny blocks Execute
//   T3 - DynamicDescription is evaluated per LLM context build
//   T4 - ContextModifier mutates AgentContext after tool execution
//   T5 - Multi-tool registry: LLM tool-call routed to correct Execute
//   T6 - Full message round-trip: tool_start->tool_end->next_turn_text

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ubuildingagent/backend/llmprovider"
)

// ── T1: ValidateInput blocks invalid args ─────────────────────────────────────

func TestToolPipeline_Unit_ValidateInput(t *testing.T) {
	tc := llmprovider.ToolCall{
		ID:        "call_vi",
		Name:      "read_file",
		Arguments: json.RawMessage(`{}`), // missing required "path" field
	}
	// LLM turn 1: calls tool with bad args; turn 2: receives error, replies with text
	mp := &mockProvider{responses: []mockTurn{
		{toolCalls: []llmprovider.ToolCall{tc}},
		{text: "I could not read the file because path was missing."},
	}}
	cleanup := setupMockRegistry(mp)
	defer cleanup()

	var executed atomic.Bool

	cfg := AgentLoopConfig{Model: llmprovider.Model{ID: "mock", Api: llmprovider.ApiOpenAICompletions}}
	conv := AgentContext{
		Messages: []AgentMessage{
			{Role: llmprovider.RoleUser, Content: []llmprovider.ContentPart{{Type: llmprovider.ContentTypeText, Text: "read the file"}}},
		},
		Tools: []AgentTool{
			{
				Name: "read_file",
				ValidateInput: func(args json.RawMessage) *ToolValidation {
					var in struct {
						Path string `json:"path"`
					}
					if err := json.Unmarshal(args, &in); err != nil || strings.TrimSpace(in.Path) == "" {
						return &ToolValidation{Valid: false, Message: "read_file: path is required"}
					}
					return &ToolValidation{Valid: true}
				},
				Execute: func(ctx *ToolExecContext) AgentToolResult {
					executed.Store(true)
					return AgentToolResult{Content: "file content"}
				},
			},
		},
	}

	ch := RunAgentLoop(context.Background(), cfg, conv)
	var toolEndEvents []AgentEvent
	for ev := range ch {
		if ev.Type == AgentEventError {
			t.Fatalf("T1: unexpected error: %v", ev.Err)
		}
		if ev.Type == AgentEventToolEnd {
			toolEndEvents = append(toolEndEvents, ev)
		}
	}

	// Execute must NOT have been called
	if executed.Load() {
		t.Error("T1: Execute was called despite ValidateInput returning Valid=false")
	}

	// ToolEnd event result must carry the validation error
	if len(toolEndEvents) == 0 {
		t.Fatal("T1: expected at least one AgentEventToolEnd")
	}
	result := toolEndEvents[0].ToolCall.Result
	if result == nil || !result.IsError {
		t.Error("T1: expected tool result IsError=true")
	}
	if result != nil && !strings.Contains(result.Content, "path is required") {
		t.Errorf("T1: expected validation message in result content, got: %q", result.Content)
	}
}

// ── T2: CheckPermission deny blocks Execute ───────────────────────────────────

func TestToolPipeline_Unit_CheckPermissionDeny(t *testing.T) {
	tc := llmprovider.ToolCall{
		ID:        "call_cp",
		Name:      "delete_file",
		Arguments: json.RawMessage(`{"path":"/tmp/x.txt"}`),
	}
	mp := &mockProvider{responses: []mockTurn{
		{toolCalls: []llmprovider.ToolCall{tc}},
		{text: "The deletion was denied."},
	}}
	cleanup := setupMockRegistry(mp)
	defer cleanup()

	var executed atomic.Bool

	cfg := AgentLoopConfig{Model: llmprovider.Model{ID: "mock", Api: llmprovider.ApiOpenAICompletions}}
	conv := AgentContext{
		Messages: []AgentMessage{
			{Role: llmprovider.RoleUser, Content: []llmprovider.ContentPart{{Type: llmprovider.ContentTypeText, Text: "delete the file"}}},
		},
		Tools: []AgentTool{
			{
				Name: "delete_file",
				CheckPermission: func(args json.RawMessage) ToolPermissionBehavior {
					return ToolPermissionDeny
				},
				Execute: func(ctx *ToolExecContext) AgentToolResult {
					executed.Store(true)
					return AgentToolResult{Content: "deleted"}
				},
			},
		},
	}

	ch := RunAgentLoop(context.Background(), cfg, conv)
	var toolEndEvents []AgentEvent
	for ev := range ch {
		if ev.Type == AgentEventError {
			t.Fatalf("T2: unexpected error: %v", ev.Err)
		}
		if ev.Type == AgentEventToolEnd {
			toolEndEvents = append(toolEndEvents, ev)
		}
	}

	if executed.Load() {
		t.Error("T2: Execute was called despite CheckPermission returning Deny")
	}
	if len(toolEndEvents) == 0 {
		t.Fatal("T2: expected AgentEventToolEnd")
	}
	result := toolEndEvents[0].ToolCall.Result
	if result == nil || !result.IsError {
		t.Error("T2: expected IsError=true for denied tool")
	}
	if result != nil && !strings.Contains(result.Content, "permission denied") {
		t.Errorf("T2: expected 'permission denied' in result, got: %q", result.Content)
	}
}

// ── T3: DynamicDescription is evaluated per LLM context build ─────────────────

func TestToolPipeline_Unit_DynamicDescription(t *testing.T) {
	// Track how many times DynamicDescription is called and what AgentContext
	// is passed so we can confirm it runs during ToLLMContext().
	var ddCallCount atomic.Int32
	var lastDescSeen string

	// Turn 1: LLM calls the tool; Turn 2: no tool, returns text
	tc := llmprovider.ToolCall{ID: "call_dd", Name: "list_items", Arguments: json.RawMessage(`{}`)}
	mp := &mockProvider{responses: []mockTurn{
		{toolCalls: []llmprovider.ToolCall{tc}},
		{text: "Items listed successfully."},
	}}
	cleanup := setupMockRegistry(mp)
	defer cleanup()

	cfg := AgentLoopConfig{Model: llmprovider.Model{ID: "mock", Api: llmprovider.ApiOpenAICompletions}}
	conv := AgentContext{
		Messages: []AgentMessage{
			{Role: llmprovider.RoleUser, Content: []llmprovider.ContentPart{{Type: llmprovider.ContentTypeText, Text: "list items"}}},
		},
		Tools: []AgentTool{
			{
				Name:        "list_items",
				Description: "static fallback description",
				Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
				DynamicDescription: func(conv *AgentContext) string {
					ddCallCount.Add(1)
					desc := "dynamic: 3 items available"
					lastDescSeen = desc
					return desc
				},
				Execute: func(ctx *ToolExecContext) AgentToolResult {
					return AgentToolResult{Content: "item1, item2, item3"}
				},
			},
		},
	}

	ch := RunAgentLoop(context.Background(), cfg, conv)
	for ev := range ch {
		if ev.Type == AgentEventError {
			t.Fatalf("T3: unexpected error: %v", ev.Err)
		}
	}

	// DynamicDescription must have been called at least once (once per LLM call)
	if ddCallCount.Load() == 0 {
		t.Error("T3: DynamicDescription was never called")
	}
	if !strings.Contains(lastDescSeen, "dynamic") {
		t.Errorf("T3: unexpected last description: %q", lastDescSeen)
	}
}

// ── T4: ContextModifier mutates AgentContext after tool execution ──────────────

func TestToolPipeline_Unit_ContextModifier(t *testing.T) {
	// Turn 1: LLM calls load_context tool.
	// Turn 2: LLM returns text (should see the hidden message injected by ContextModifier).
	tc := llmprovider.ToolCall{ID: "call_cm", Name: "load_context", Arguments: json.RawMessage(`{}`)}

	// Capture what messages the mock LLM receives on each call.
	var capturedContexts []llmprovider.Context

	mp := &mockProvider{responses: []mockTurn{
		{toolCalls: []llmprovider.ToolCall{tc}},
		{text: "The secret is XK-9271."},
	}}
	cleanup := setupMockRegistry(mp)
	defer cleanup()

	const hiddenSecret = "[HIDDEN] SECRET_CODE=XK-9271"

	cfg := AgentLoopConfig{
		Model: llmprovider.Model{ID: "mock", Api: llmprovider.ApiOpenAICompletions},
		// Intercept every LLM context to verify injected hidden messages
		TransformContext: func(ctx llmprovider.Context) llmprovider.Context {
			capturedContexts = append(capturedContexts, ctx)
			return ctx
		},
	}
	conv := AgentContext{
		Messages: []AgentMessage{
			{Role: llmprovider.RoleUser, Content: []llmprovider.ContentPart{{Type: llmprovider.ContentTypeText, Text: "load context"}}},
		},
		Tools: []AgentTool{
			{
				Name: "load_context",
				Execute: func(ctx *ToolExecContext) AgentToolResult {
					return AgentToolResult{
						Content: "context loaded",
						ContextModifier: func(conv *AgentContext) {
							conv.Messages = append(conv.Messages, AgentMessage{
								Role: llmprovider.RoleSystem,
								Content: []llmprovider.ContentPart{
									{Type: llmprovider.ContentTypeText, Text: hiddenSecret},
								},
							})
						},
					}
				},
			},
		},
	}

	ch := RunAgentLoop(context.Background(), cfg, conv)
	for ev := range ch {
		if ev.Type == AgentEventError {
			t.Fatalf("T4: unexpected error: %v", ev.Err)
		}
	}

	// Turn 2's llmCtx must contain the hidden message injected by ContextModifier.
	// capturedContexts[0] = turn 1 (before tool call), capturedContexts[1] = turn 2.
	if len(capturedContexts) < 2 {
		t.Fatalf("T4: expected at least 2 LLM calls, got %d", len(capturedContexts))
	}
	turn2 := capturedContexts[1]
	foundHidden := false
	for _, msg := range turn2.Messages {
		for _, part := range msg.Content {
			if strings.Contains(part.Text, "XK-9271") {
				foundHidden = true
			}
		}
	}
	if !foundHidden {
		t.Error("T4: hidden message injected by ContextModifier was not present in turn-2 LLM context")
	}
}

// ── T5: Multi-tool routing — LLM routes to correct Execute ───────────────────

func TestToolPipeline_Unit_MultiToolRouting(t *testing.T) {
	// Mock LLM calls "add_numbers" specifically
	tc := llmprovider.ToolCall{
		ID:        "call_add",
		Name:      "add_numbers",
		Arguments: json.RawMessage(`{"a":17,"b":25}`),
	}
	mp := &mockProvider{responses: []mockTurn{
		{toolCalls: []llmprovider.ToolCall{tc}},
		{text: "The answer is 42."},
	}}
	cleanup := setupMockRegistry(mp)
	defer cleanup()

	var calledTools []string
	makeExec := func(name string) func(*ToolExecContext) AgentToolResult {
		return func(ctx *ToolExecContext) AgentToolResult {
			calledTools = append(calledTools, name)
			if name == "add_numbers" {
				return AgentToolResult{Content: "42"}
			}
			return AgentToolResult{Content: "unexpected call to " + name}
		}
	}

	cfg := AgentLoopConfig{Model: llmprovider.Model{ID: "mock", Api: llmprovider.ApiOpenAICompletions}}
	conv := AgentContext{
		Messages: []AgentMessage{
			{Role: llmprovider.RoleUser, Content: []llmprovider.ContentPart{{Type: llmprovider.ContentTypeText, Text: "what is 17+25?"}}},
		},
		Tools: []AgentTool{
			{Name: "add_numbers", Execute: makeExec("add_numbers")},
			{Name: "get_time", Execute: makeExec("get_time")},
			{Name: "echo_text", Execute: makeExec("echo_text")},
		},
	}

	ch := RunAgentLoop(context.Background(), cfg, conv)
	var reply string
	for ev := range ch {
		if ev.Type == AgentEventError {
			t.Fatalf("T5: unexpected error: %v", ev.Err)
		}
		if ev.Type == AgentEventTextDelta {
			reply += ev.Delta
		}
	}

	if len(calledTools) == 0 {
		t.Fatal("T5: no tool was called")
	}
	if calledTools[0] != "add_numbers" {
		t.Errorf("T5: expected add_numbers to be called first, got: %v", calledTools)
	}
	if len(calledTools) > 1 {
		t.Errorf("T5: expected exactly 1 tool call, got: %v", calledTools)
	}
	if !strings.Contains(reply, "42") {
		t.Errorf("T5: expected '42' in final reply, got: %q", reply)
	}
}

// ── T6: Full message round-trip event sequence ────────────────────────────────

func TestToolPipeline_Unit_MessageRoundTrip(t *testing.T) {
	tc := llmprovider.ToolCall{
		ID:        "call_wx",
		Name:      "get_weather",
		Arguments: json.RawMessage(`{"city":"Beijing"}`),
	}
	mp := &mockProvider{responses: []mockTurn{
		{toolCalls: []llmprovider.ToolCall{tc}},
		{text: "Beijing is sunny and 22 degrees."},
	}}
	cleanup := setupMockRegistry(mp)
	defer cleanup()

	cfg := AgentLoopConfig{Model: llmprovider.Model{ID: "mock", Api: llmprovider.ApiOpenAICompletions}}
	conv := AgentContext{
		Messages: []AgentMessage{
			{Role: llmprovider.RoleUser, Content: []llmprovider.ContentPart{{Type: llmprovider.ContentTypeText, Text: "weather in Beijing?"}}},
		},
		Tools: []AgentTool{
			{
				Name: "get_weather",
				Execute: func(ctx *ToolExecContext) AgentToolResult {
					return AgentToolResult{Content: `{"temperature":"22C","condition":"sunny"}`}
				},
			},
		},
	}

	ch := RunAgentLoop(context.Background(), cfg, conv)

	// Collect all events in order
	var events []AgentEvent
	for ev := range ch {
		if ev.Type == AgentEventError {
			t.Fatalf("T6: unexpected error: %v", ev.Err)
		}
		events = append(events, ev)
	}

	// Verify required event types are present
	has := func(et AgentEventType) bool {
		for _, e := range events {
			if e.Type == et {
				return true
			}
		}
		return false
	}
	for _, required := range []AgentEventType{
		AgentEventStart,
		AgentEventTurnStart,
		AgentEventToolStart,
		AgentEventToolEnd,
		AgentEventTurnEnd,
		AgentEventEnd,
	} {
		if !has(required) {
			t.Errorf("T6: missing event type %v", required)
		}
	}

	// Verify ordering: last tool_end must precede final text_delta
	lastToolEndIdx := -1
	for i, ev := range events {
		if ev.Type == AgentEventToolEnd {
			lastToolEndIdx = i
		}
	}
	hasTextAfterTool := false
	for i := lastToolEndIdx + 1; i < len(events); i++ {
		if events[i].Type == AgentEventTextDelta {
			hasTextAfterTool = true
			break
		}
	}
	if lastToolEndIdx < 0 {
		t.Fatal("T6: no AgentEventToolEnd found")
	}
	if !hasTextAfterTool {
		t.Error("T6: expected text_delta after the final tool_end (round-trip not complete)")
	}

	// Final reply should contain the mock text
	var reply string
	for _, ev := range events {
		if ev.Type == AgentEventTextDelta {
			reply += ev.Delta
		}
	}
	if !strings.Contains(reply, "22") {
		t.Errorf("T6: expected tool output to appear in final reply, got: %q", reply)
	}
}
