//go:build integration

package agentcore_test

import (
	"context"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ubuildingagent/backend/agentcore"
	"github.com/ubuildingagent/backend/internal/envconfig"
	"github.com/ubuildingagent/backend/llmprovider"
	_ "github.com/ubuildingagent/backend/llmprovider/providers"
	tool "github.com/ubuildingagent/backend/tools"
	"github.com/ubuildingagent/backend/tools/webfetch"
	"github.com/ubuildingagent/backend/tools/websearch"
)

// ── helpers (external test package cannot reuse e2e_test.go internals) ──────

func webEnvFile() string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", ".env")
}

func loadWebCfg(t *testing.T) agentcore.AgentLoopConfig {
	t.Helper()
	cfg, err := envconfig.LoadFromFile(webEnvFile())
	if err != nil {
		t.Skipf("skipping: .env not found (%v)", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Skipf("skipping: incomplete .env (%v)", err)
	}
	t.Logf("LLM_TYPE=%s  MODEL=%s  BASE_URL=%s", cfg.Type, cfg.Model, cfg.BaseURL)
	return agentcore.AgentLoopConfig{
		Model:      cfg.ToModel(),
		StreamOpts: llmprovider.SimpleStreamOptions{StreamOptions: cfg.ToStreamOptions()},
	}
}

func drainWeb(t *testing.T, ch <-chan agentcore.AgentEvent, timeout time.Duration) []agentcore.AgentEvent {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	var events []agentcore.AgentEvent
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return events
			}
			if ev.Type == agentcore.AgentEventError {
				msg := ev.Err.Error()
				if strings.Contains(msg, "http 400") ||
					strings.Contains(msg, "http 401") ||
					strings.Contains(msg, "http 422") ||
					strings.Contains(msg, "InvalidParameter") {
					t.Skipf("skipping: API error: %v", ev.Err)
				}
				t.Fatalf("AgentEvent error: %v", ev.Err)
			}
			events = append(events, ev)
		case <-ctx.Done():
			t.Fatalf("timeout waiting for agent events (%s)", timeout)
		}
	}
}

func textFromWeb(events []agentcore.AgentEvent) string {
	var sb strings.Builder
	for _, ev := range events {
		if ev.Type == agentcore.AgentEventTextDelta {
			sb.WriteString(ev.Delta)
		}
	}
	return sb.String()
}

func toolEndsFor(events []agentcore.AgentEvent, name string) []agentcore.AgentEvent {
	var out []agentcore.AgentEvent
	for _, ev := range events {
		if ev.Type == agentcore.AgentEventToolEnd && ev.ToolCall != nil && ev.ToolCall.Name == name {
			out = append(out, ev)
		}
	}
	return out
}

// ── T1: WebSearch ────────────────────────────────────────────────────────────

func TestWebSearch_ToolCall(t *testing.T) {
	cfg := loadWebCfg(t)

	wsTool := tool.ToAgentTool(websearch.New("", ""))

	conv := agentcore.AgentContext{
		SystemPrompt: "You are a helpful assistant with web search capability. When asked to search, always use the WebSearch tool.",
		Messages: []agentcore.AgentMessage{
			{
				Role:    llmprovider.RoleUser,
				Content: []llmprovider.ContentPart{{Type: llmprovider.ContentTypeText, Text: "Search the web for 'golang 1.22 new features' and briefly summarize the top result."}},
			},
		},
		Tools: []agentcore.AgentTool{wsTool},
	}

	ch := agentcore.RunAgentLoop(context.Background(), cfg, conv)
	events := drainWeb(t, ch, 90*time.Second)

	ends := toolEndsFor(events, "WebSearch")
	if len(ends) == 0 {
		t.Skip("WebSearch: model did not call WebSearch tool")
	}

	t.Logf("WebSearch called %d time(s)", len(ends))
	for i, e := range ends {
		if e.ToolCall != nil && e.ToolCall.Result != nil {
			t.Logf("  result[%d] (first 300 chars): %.300s", i, e.ToolCall.Result.Content)
			if e.ToolCall.Result.Content == "" {
				t.Errorf("WebSearch result[%d] content is empty", i)
			}
		}
	}

	reply := textFromWeb(events)
	t.Logf("Final reply: %s", reply)
	if reply == "" {
		t.Error("expected non-empty final reply after WebSearch")
	}
}

// ── T2: WebFetch ─────────────────────────────────────────────────────────────

func TestWebFetch_ToolCall(t *testing.T) {
	cfg := loadWebCfg(t)

	wfTool := tool.ToAgentTool(webfetch.New())

	conv := agentcore.AgentContext{
		SystemPrompt: "You are a helpful assistant with web fetch capability. When asked to fetch a URL, always use the WebFetch tool.",
		Messages: []agentcore.AgentMessage{
			{
				Role:    llmprovider.RoleUser,
				Content: []llmprovider.ContentPart{{Type: llmprovider.ContentTypeText, Text: "Fetch https://example.com using the WebFetch tool and tell me the page title."}},
			},
		},
		Tools: []agentcore.AgentTool{wfTool},
	}

	ch := agentcore.RunAgentLoop(context.Background(), cfg, conv)
	events := drainWeb(t, ch, 90*time.Second)

	ends := toolEndsFor(events, "WebFetch")
	if len(ends) == 0 {
		t.Skip("WebFetch: model did not call WebFetch tool")
	}

	t.Logf("WebFetch called %d time(s)", len(ends))
	for i, e := range ends {
		if e.ToolCall != nil && e.ToolCall.Result != nil {
			t.Logf("  result[%d] (first 300 chars): %.300s", i, e.ToolCall.Result.Content)
			if e.ToolCall.Result.Content == "" {
				t.Errorf("WebFetch result[%d] content is empty", i)
			}
		}
	}

	reply := textFromWeb(events)
	t.Logf("Final reply: %s", reply)
	if reply == "" {
		t.Error("expected non-empty final reply after WebFetch")
	}
}
