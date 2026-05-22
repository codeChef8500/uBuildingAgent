//go:build integration

package agentcore

// e2e_debug_fc_test.go — diagnoses why function-calling fails on the .env endpoint.
//
// Run: go test -tags integration -v -run TestDebugFunctionCalling ./agentcore/... -timeout 60s

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ubuildingagent/backend/internal/envconfig"
	"github.com/ubuildingagent/backend/llmprovider"
)

// loadEnvRaw reads .env and returns (apiKey, baseURL, modelID).
func loadEnvRaw(t *testing.T) (apiKey, baseURL, modelID string) {
	t.Helper()
	_, thisFile, _, _ := runtime.Caller(0)
	envPath := filepath.Join(filepath.Dir(thisFile), "..", ".env")
	cfg, err := envconfig.LoadFromFile(envPath)
	if err != nil {
		t.Skipf("skipping: .env not found (%v)", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Skipf("skipping: invalid .env (%v)", err)
	}
	return cfg.APIKey, cfg.BaseURL, cfg.Model
}

// rawChat sends a single OpenAI-format chat completion request and returns
// the raw response body + status code.
func rawChat(t *testing.T, apiKey, baseURL, body string) (int, string) {
	t.Helper()
	url := strings.TrimRight(baseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(
		context.Background(), http.MethodPost, url,
		bytes.NewBufferString(body),
	)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+apiKey)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("http do: %v", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(raw)
}

// pp pretty-prints a JSON string (best-effort).
func pp(s string) string {
	var v any
	if err := json.Unmarshal([]byte(s), &v); err != nil {
		return s
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

// TestDebugLoopPayload runs a real agentcore loop with OnPayload to capture
// the exact request body the loop sends, so we can compare it against the
// working raw-HTTP variants in TestDebugFunctionCalling.
func TestDebugLoopPayload(t *testing.T) {
	apiKey, baseURL, modelID := loadEnvRaw(t)

	var (
		capturedPayloads []string
		capturedCodes    []int
	)

	cfg := loadAgentConfig(t)
	// Attach OnPayload + OnResponse hooks to capture every outgoing request
	cfg.StreamOpts.OnPayload = func(payload []byte, model llmprovider.Model) []byte {
		capturedPayloads = append(capturedPayloads, string(payload))
		return nil // pass through unchanged
	}
	cfg.StreamOpts.OnResponse = func(code int, _ http.Header, _ llmprovider.Model) {
		capturedCodes = append(capturedCodes, code)
	}

	_ = apiKey
	_ = baseURL
	_ = modelID

	conv := AgentContext{
		SystemPrompt: "You are a math assistant. Use add_numbers when asked to compute a sum.",
		Messages: []AgentMessage{
			{Role: llmprovider.RoleUser, Content: []llmprovider.ContentPart{{Type: llmprovider.ContentTypeText, Text: "Use the add_numbers tool to compute 17 + 25."}}},
		},
		Tools: []AgentTool{
			{
				Name:        "add_numbers",
				Description: "Add two numbers a and b.",
				Parameters:  json.RawMessage(`{"type":"object","properties":{"a":{"type":"number"},"b":{"type":"number"}},"required":["a","b"]}`),
				Execute: func(ctx *ToolExecContext) AgentToolResult {
					t.Logf("Execute called with args: %s", string(ctx.Args))
					return AgentToolResult{Content: "42"}
				},
			},
		},
	}

	ch := RunAgentLoop(context.Background(), cfg, conv)
	for ev := range ch {
		if ev.Type == AgentEventError {
			t.Logf("Loop error: %v", ev.Err)
		}
	}

	t.Logf("Total HTTP calls: %d", len(capturedPayloads))
	for i, payload := range capturedPayloads {
		code := 0
		if i < len(capturedCodes) {
			code = capturedCodes[i]
		}
		t.Logf("── Call %d (HTTP %d) ──────────────────", i+1, code)
		t.Logf("REQUEST BODY:\n%s", pp(payload))
	}
}

// TestDebugContextModifierPayload runs two Agent.Prompt() turns with a
// ContextModifier that injects a system message and dumps the payloads to
// verify the secret actually reaches the LLM on turn 2.
func TestDebugContextModifierPayload(t *testing.T) {
	_, _, _ = loadEnvRaw(t)

	var (
		capturedPayloads []string
		capturedCodes    []int
	)

	cfg := loadAgentConfig(t)
	cfg.StreamOpts.OnPayload = func(payload []byte, model llmprovider.Model) []byte {
		capturedPayloads = append(capturedPayloads, string(payload))
		return nil
	}
	cfg.StreamOpts.OnResponse = func(code int, _ http.Header, _ llmprovider.Model) {
		capturedCodes = append(capturedCodes, code)
	}

	const secret = "[CONTEXT] SECRET_CODE=XK-9271"

	loadCtx := AgentTool{
		Name:        "load_context",
		Description: "Load a context blob into memory.",
		Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
		Execute: func(ctx *ToolExecContext) AgentToolResult {
			return AgentToolResult{
				Content: "Context loaded.",
				ContextModifier: func(conv *AgentContext) {
					conv.Messages = append(conv.Messages, AgentMessage{
						Role:    llmprovider.RoleSystem,
						Content: []llmprovider.ContentPart{{Type: llmprovider.ContentTypeText, Text: secret}},
					})
				},
			}
		},
	}

	ag := NewAgent(cfg, "You are a helpful assistant.")
	ag.AddTool(loadCtx)

	// Turn 1
	for ev := range ag.Prompt(context.Background(), "Call load_context now.") {
		if ev.Type == AgentEventError {
			t.Logf("T1 error: %v", ev.Err)
		}
	}

	// Turn 2 — ask about secret
	var turn2Reply string
	for ev := range ag.Prompt(context.Background(), "What is the SECRET_CODE from the loaded context?") {
		if ev.Type == AgentEventError {
			t.Logf("T2 error: %v", ev.Err)
		}
		if ev.Type == AgentEventTextDelta {
			turn2Reply += ev.Delta
		}
	}

	t.Logf("Turn2 reply: %s", turn2Reply)
	t.Logf("Total HTTP calls: %d", len(capturedPayloads))
	for i, payload := range capturedPayloads {
		code := 0
		if i < len(capturedCodes) {
			code = capturedCodes[i]
		}
		t.Logf("── Call %d (HTTP %d) ──────────────────", i+1, code)
		// Extract only the messages for readability
		var body map[string]any
		if err := json.Unmarshal([]byte(payload), &body); err == nil {
			if msgs, ok := body["messages"]; ok {
				b, _ := json.MarshalIndent(msgs, "", "  ")
				t.Logf("messages:\n%s", string(b))
			}
		}
	}
}

// TestDebugFunctionCalling sends progressively richer requests to the .env
// endpoint and logs the exact request body and response for each variant.
// This isolates which specific field(s) cause HTTP 400 for function calls.
func TestDebugFunctionCalling(t *testing.T) {
	apiKey, baseURL, modelID := loadEnvRaw(t)
	t.Logf("Endpoint: %s  Model: %s", baseURL, modelID)

	simpleUserMsg := []map[string]any{
		{"role": "user", "content": "What is 2+2?"},
	}

	tool1 := map[string]any{
		"type": "function",
		"function": map[string]any{
			"name":        "add_numbers",
			"description": "Add two numbers",
			"parameters": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"a": map[string]any{"type": "number"},
					"b": map[string]any{"type": "number"},
				},
				"required": []string{"a", "b"},
			},
		},
	}

	cases := []struct {
		name string
		body map[string]any
	}{
		{
			// Baseline: no tools, just text (should succeed)
			name: "1_no_tools",
			body: map[string]any{
				"model":    modelID,
				"stream":   false,
				"messages": simpleUserMsg,
			},
		},
		{
			// Add stream_options (as the provider sends for usage)
			name: "2_no_tools_stream_options",
			body: map[string]any{
				"model":          modelID,
				"stream":         true,
				"messages":       simpleUserMsg,
				"stream_options": map[string]any{"include_usage": true},
			},
		},
		{
			// Minimal tools, no tool_choice, no strict
			name: "3_tools_no_tool_choice",
			body: map[string]any{
				"model":    modelID,
				"stream":   false,
				"messages": simpleUserMsg,
				"tools":    []any{tool1},
			},
		},
		{
			// Add tool_choice: "auto"
			name: "4_tools_tool_choice_auto",
			body: map[string]any{
				"model":       modelID,
				"stream":      false,
				"messages":    simpleUserMsg,
				"tools":       []any{tool1},
				"tool_choice": "auto",
			},
		},
		{
			// Stream mode + tools + tool_choice (closest to what the provider sends)
			name: "5_stream_tools_tool_choice",
			body: map[string]any{
				"model":          modelID,
				"stream":         true,
				"messages":       simpleUserMsg,
				"tools":          []any{tool1},
				"tool_choice":    "auto",
				"stream_options": map[string]any{"include_usage": true},
			},
		},
		{
			// Same but force the model to call the tool
			name: "6_stream_tools_tool_choice_required",
			body: map[string]any{
				"model":       modelID,
				"stream":      true,
				"messages":    simpleUserMsg,
				"tools":       []any{tool1},
				"tool_choice": "required",
			},
		},
		{
			// tool with strict: true (OpenAI-style)
			name: "7_tools_strict_true",
			body: map[string]any{
				"model":    modelID,
				"stream":   false,
				"messages": simpleUserMsg,
				"tools": []any{map[string]any{
					"type": "function",
					"function": map[string]any{
						"name":        "add_numbers",
						"description": "Add two numbers",
						"strict":      true,
						"parameters": map[string]any{
							"type": "object",
							"properties": map[string]any{
								"a": map[string]any{"type": "number"},
								"b": map[string]any{"type": "number"},
							},
							"required":             []string{"a", "b"},
							"additionalProperties": false,
						},
					},
				}},
				"tool_choice": "auto",
			},
		},
		{
			// Full round-trip: history already contains assistant tool_calls + tool result
			// This replicates the exact second call the agentcore loop makes.
			name: "9_full_round_trip_with_tool_result_history",
			body: map[string]any{
				"model":  modelID,
				"stream": true,
				"messages": []map[string]any{
					{"role": "user", "content": "Use add_numbers to compute 17 + 25."},
					{
						"role":    "assistant",
						"content": nil,
						"tool_calls": []map[string]any{
							{
								"id":   "call_test_001",
								"type": "function",
								"function": map[string]any{
									"name":      "add_numbers",
									"arguments": `{"a":17,"b":25}`,
								},
							},
						},
					},
					{
						"role":         "tool",
						"tool_call_id": "call_test_001",
						"content":      "42",
					},
				},
				"tools":          []any{tool1},
				"tool_choice":    "auto",
				"stream_options": map[string]any{"include_usage": true},
			},
		},
		{
			// Prompt explicitly asking for tool call, ask model to call the tool
			name: "8_stream_tools_explicit_prompt",
			body: map[string]any{
				"model":  modelID,
				"stream": true,
				"messages": []map[string]any{
					{"role": "user", "content": "Use the add_numbers tool to compute 17 + 25."},
				},
				"tools":       []any{tool1},
				"tool_choice": "auto",
			},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			bodyBytes, _ := json.MarshalIndent(c.body, "", "  ")
			t.Logf("REQUEST BODY:\n%s", string(bodyBytes))

			code, respBody := rawChat(t, apiKey, baseURL, string(bodyBytes))

			if code != 200 {
				t.Logf("RESPONSE [HTTP %d]:\n%s", code, pp(respBody))
				t.Logf("RESULT: FAILED (HTTP %d)", code)
			} else {
				// For streaming responses, show first 500 chars
				preview := respBody
				if len(preview) > 500 {
					preview = preview[:500] + "...[truncated]"
				}
				t.Logf("RESPONSE [HTTP %d]:\n%s", code, preview)
				t.Logf("RESULT: OK")

				// Check if response contains tool_calls
				if strings.Contains(respBody, "tool_calls") {
					t.Logf("  ** CONTAINS tool_calls — function calling WORKS on this variant **")
				} else if strings.Contains(respBody, "finish_reason") {
					t.Logf("  ** finish_reason found; model responded without tool call **")
				}
			}

			// Print a separator
			t.Logf(strings.Repeat("-", 60))

			// Extract error message for 4xx
			if code >= 400 {
				var errResp struct {
					Error struct {
						Code    string `json:"code"`
						Message string `json:"message"`
						Param   string `json:"param"`
						Type    string `json:"type"`
					} `json:"error"`
				}
				if err := json.Unmarshal([]byte(respBody), &errResp); err == nil {
					t.Logf("ERROR CODE: %s", errResp.Error.Code)
					t.Logf("ERROR MSG:  %s", errResp.Error.Message)
					t.Logf("ERROR PARAM: %s", errResp.Error.Param)
				}
				fmt.Printf("[FAIL] %s — HTTP %d: %s\n", c.name, code, errResp.Error.Message)
			} else {
				fmt.Printf("[OK]   %s — HTTP %d\n", c.name, code)
			}
		})
	}
}
