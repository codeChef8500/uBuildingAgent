package openaicompat

import (
	"encoding/json"
	"testing"

	"github.com/ubuildingagent/backend/llmprovider"
)

func TestConvertMessages_SystemOnly(t *testing.T) {
	compat := llmprovider.OpenAICompletionsCompat{
		SupportsDeveloperRole: llmprovider.BoolPtr(false),
	}
	msgs := ConvertMessages(nil, "You are helpful.", compat)
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0]["role"] != "system" {
		t.Errorf("role: got %q", msgs[0]["role"])
	}
}

func TestConvertMessages_DeveloperRole(t *testing.T) {
	compat := llmprovider.OpenAICompletionsCompat{
		SupportsDeveloperRole: llmprovider.BoolPtr(true),
	}
	msgs := ConvertMessages(nil, "system prompt", compat)
	if msgs[0]["role"] != "developer" {
		t.Errorf("expected developer role, got %q", msgs[0]["role"])
	}
}

func TestConvertMessages_UserText(t *testing.T) {
	messages := []llmprovider.Message{
		{
			Role:    llmprovider.RoleUser,
			Content: []llmprovider.ContentPart{{Type: llmprovider.ContentTypeText, Text: "Hello"}},
		},
	}
	msgs := ConvertMessages(messages, "", llmprovider.OpenAICompletionsCompat{})
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0]["content"] != "Hello" {
		t.Errorf("content: got %v", msgs[0]["content"])
	}
}

func TestConvertMessages_AssistantToolCall(t *testing.T) {
	messages := []llmprovider.Message{
		{
			Role: llmprovider.RoleAssistant,
			ToolCalls: []llmprovider.ToolCall{
				{
					ID:        "call_1",
					Name:      "get_weather",
					Arguments: json.RawMessage(`{"location":"NYC"}`),
				},
			},
		},
	}
	msgs := ConvertMessages(messages, "", llmprovider.OpenAICompletionsCompat{})
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	tc := msgs[0]["tool_calls"].([]map[string]any)
	if len(tc) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(tc))
	}
	if tc[0]["id"] != "call_1" {
		t.Errorf("tool_call id: got %v", tc[0]["id"])
	}
}

func TestConvertMessages_ToolResult(t *testing.T) {
	messages := []llmprovider.Message{
		{
			Role: llmprovider.RoleTool,
			Content: []llmprovider.ContentPart{
				{
					Type:       llmprovider.ContentTypeToolResult,
					ToolCallID: "call_1",
					ToolResult: `{"temp":72}`,
				},
			},
		},
	}
	msgs := ConvertMessages(messages, "", llmprovider.OpenAICompletionsCompat{})
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0]["role"] != "tool" {
		t.Errorf("role: got %q", msgs[0]["role"])
	}
	if msgs[0]["tool_call_id"] != "call_1" {
		t.Errorf("tool_call_id: got %v", msgs[0]["tool_call_id"])
	}
}

func TestConvertMessages_RequiresToolResultName(t *testing.T) {
	messages := []llmprovider.Message{
		{
			Role: llmprovider.RoleTool,
			Content: []llmprovider.ContentPart{
				{Type: llmprovider.ContentTypeToolResult, ToolCallID: "abc", ToolResult: "result"},
			},
		},
	}
	compat := llmprovider.OpenAICompletionsCompat{
		RequiresToolResultName: llmprovider.BoolPtr(true),
	}
	msgs := ConvertMessages(messages, "", compat)
	if _, ok := msgs[0]["name"]; !ok {
		t.Error("expected 'name' field when RequiresToolResultName=true")
	}
}

func TestConvertMessages_ThinkingAsText(t *testing.T) {
	messages := []llmprovider.Message{
		{
			Role:     llmprovider.RoleAssistant,
			Thinking: "Let me think...",
			Content:  []llmprovider.ContentPart{{Type: llmprovider.ContentTypeText, Text: "Answer"}},
		},
	}
	compat := llmprovider.OpenAICompletionsCompat{
		RequiresThinkingAsText: llmprovider.BoolPtr(true),
	}
	msgs := ConvertMessages(messages, "", compat)
	content, ok := msgs[0]["content"].(string)
	if !ok {
		t.Fatal("expected string content")
	}
	if content[:9] != "<thinking" {
		t.Errorf("expected thinking prefix, got: %s", content[:30])
	}
}

func TestConvertTools_Basic(t *testing.T) {
	tools := []llmprovider.Tool{
		{
			Name:        "get_weather",
			Description: "Get current weather",
			Parameters:  json.RawMessage(`{"type":"object","properties":{"location":{"type":"string"}}}`),
		},
	}
	compat := llmprovider.OpenAICompletionsCompat{
		SupportsStrictMode: llmprovider.BoolPtr(true),
	}
	out := ConvertTools(tools, compat)
	if len(out) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(out))
	}
	fn := out[0]["function"].(map[string]any)
	if fn["name"] != "get_weather" {
		t.Errorf("name: got %v", fn["name"])
	}
	if fn["strict"] != true {
		t.Error("expected strict=true")
	}
}

func TestBuildReasoningParams_DeepSeek(t *testing.T) {
	body := map[string]any{}
	compat := llmprovider.OpenAICompletionsCompat{ThinkingFormat: llmprovider.ThinkingFormatDeepSeek}
	BuildReasoningParams(body, llmprovider.ThinkingLevelMedium, 8192, compat)

	if _, ok := body["thinking"]; !ok {
		t.Error("expected 'thinking' field for DeepSeek")
	}
	th := body["thinking"].(map[string]any)
	if th["budget_tokens"] != 8192 {
		t.Errorf("budget_tokens: got %v", th["budget_tokens"])
	}
	if body["reasoning_effort"] != "medium" {
		t.Errorf("reasoning_effort: got %v", body["reasoning_effort"])
	}
}

func TestBuildReasoningParams_QwenEnable(t *testing.T) {
	body := map[string]any{}
	compat := llmprovider.OpenAICompletionsCompat{ThinkingFormat: llmprovider.ThinkingFormatQwen}
	BuildReasoningParams(body, llmprovider.ThinkingLevelHigh, 16384, compat)

	if body["enable_thinking"] != true {
		t.Errorf("enable_thinking: got %v", body["enable_thinking"])
	}
}

func TestBuildReasoningParams_Off(t *testing.T) {
	body := map[string]any{}
	compat := llmprovider.OpenAICompletionsCompat{ThinkingFormat: llmprovider.ThinkingFormatOpenAI}
	BuildReasoningParams(body, llmprovider.ThinkingLevelOff, 0, compat)
	if len(body) != 0 {
		t.Errorf("expected empty body for ThinkingLevelOff, got %v", body)
	}
}
