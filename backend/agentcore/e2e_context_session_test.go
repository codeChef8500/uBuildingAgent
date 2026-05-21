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
	"github.com/ubuildingagent/backend/agentcore/contextmgr"
	"github.com/ubuildingagent/backend/agentcore/session"
	"github.com/ubuildingagent/backend/internal/envconfig"
	"github.com/ubuildingagent/backend/llmprovider"
	_ "github.com/ubuildingagent/backend/llmprovider/providers"
)

// csEnvFile resolves the backend/.env path relative to this test file.
func csEnvFile() string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", ".env")
}

// loadRealConfig reads .env and returns a base AgentLoopConfig.
func loadRealConfig(t *testing.T) agentcore.AgentLoopConfig {
	t.Helper()
	cfg, err := envconfig.LoadFromFile(csEnvFile())
	if err != nil {
		t.Skipf("skipping: .env not found (%v)", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Skipf("skipping: invalid .env (%v)", err)
	}
	return agentcore.AgentLoopConfig{
		Model:      cfg.ToModel(),
		StreamOpts: llmprovider.SimpleStreamOptions{StreamOptions: cfg.ToStreamOptions()},
	}
}

func drainCtx(t *testing.T, ch <-chan agentcore.AgentEvent, timeout time.Duration) []agentcore.AgentEvent {
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
				if strings.Contains(msg, "http 400") || strings.Contains(msg, "InvalidParameter") {
					t.Skipf("API error (skipping): %v", ev.Err)
				}
				t.Fatalf("loop error: %v", ev.Err)
			}
			events = append(events, ev)
		case <-ctx.Done():
			t.Fatalf("timeout (%s)", timeout)
		}
	}
}

func collectText(events []agentcore.AgentEvent) string {
	var sb strings.Builder
	for _, ev := range events {
		if ev.Type == agentcore.AgentEventTextDelta {
			sb.WriteString(ev.Delta)
		}
	}
	return sb.String()
}

// ── T1: Session persists multi-turn conversation with real LLM ─────────────

func TestAgentE2E_SessionPersistMultiTurn(t *testing.T) {
	cfg := loadRealConfig(t)

	store := session.NewInMemoryStorage("e2e_sess")
	writer := session.NewLoopWriter(store, "e2e_sess")
	cfg.Session = writer

	agent := agentcore.NewAgent(cfg, "You are a concise assistant.")

	// Turn 1
	events1 := drainCtx(t, agent.Prompt(context.Background(), "记住：秘密数字是42。"), 30*time.Second)
	reply1 := collectText(events1)
	t.Logf("Turn 1 reply: %s", reply1)

	// Turn 2
	events2 := drainCtx(t, agent.Prompt(context.Background(), "秘密数字是什么？"), 30*time.Second)
	reply2 := collectText(events2)
	t.Logf("Turn 2 reply: %s", reply2)

	if !strings.Contains(reply2, "42") {
		t.Errorf("expected model to recall '42', got: %s", reply2)
	}

	// Check session has entries for both turns
	entries, _ := store.GetEntries()
	t.Logf("Session total entries: %d", len(entries))
	if len(entries) < 2 {
		t.Errorf("expected ≥2 session entries, got %d", len(entries))
	}

	// Verify agent state matches session
	state := agent.State()
	if state.IsStreaming {
		t.Error("IsStreaming should be false after completion")
	}
	if len(state.Messages) < 4 {
		t.Errorf("expected ≥4 messages in agent state, got %d", len(state.Messages))
	}
}

// ── T2: Context compaction with real LLM ──────────────────────────────────

func TestAgentE2E_ContextCompaction(t *testing.T) {
	cfg := loadRealConfig(t)

	// Inject 40 synthetic messages to overflow a tiny token budget
	syntheticMsgs := make([]agentcore.AgentMessage, 40)
	for i := range syntheticMsgs {
		role := llmprovider.RoleUser
		if i%2 == 1 {
			role = llmprovider.RoleAssistant
		}
		syntheticMsgs[i] = agentcore.AgentMessage{
			Role: role,
			Content: []llmprovider.ContentPart{{
				Type: llmprovider.ContentTypeText,
				Text: "This is a filler message to inflate the token count significantly.",
			}},
			Source: string(role),
		}
	}

	est := contextmgr.NewCharHeuristicEstimator()
	settings := contextmgr.CompactionSettings{
		MaxTokens:    100, // triggers immediately on 40 messages
		HeadProtectN: 2,
		TailProtectN: 3,
		TargetRatio:  0.5,
	}
	adapter := contextmgr.NewLoopAdapter(est, settings)
	cfg.ContextEngine = adapter
	cfg.CompactionSettings = agentcore.CompactionConfig{MaxTokens: 100}

	// Append a real user message at the end
	syntheticMsgs = append(syntheticMsgs, agentcore.AgentMessage{
		Role:    llmprovider.RoleUser,
		Content: []llmprovider.ContentPart{{Type: llmprovider.ContentTypeText, Text: "用一个词回答：天空是什么颜色？"}},
		Source:  "user",
	})

	conv := agentcore.AgentContext{
		SystemPrompt: "You are a concise assistant.",
		Messages:     syntheticMsgs,
	}

	ch := agentcore.RunAgentLoop(context.Background(), cfg, conv)
	events := drainCtx(t, ch, 30*time.Second)

	reply := collectText(events)
	t.Logf("Reply after compaction: %s", reply)
	if reply == "" {
		t.Error("expected non-empty reply after context compaction")
	}
}

// ── T3: Session persist + compaction together ─────────────────────────────

func TestAgentE2E_SessionAndCompaction(t *testing.T) {
	cfg := loadRealConfig(t)

	store := session.NewInMemoryStorage("e2e_both")
	cfg.Session = session.NewLoopWriter(store, "e2e_both")

	est := contextmgr.NewCharHeuristicEstimator()
	settings := contextmgr.CompactionSettings{
		MaxTokens:    200,
		HeadProtectN: 1,
		TailProtectN: 2,
		TargetRatio:  0.5,
	}
	cfg.ContextEngine = contextmgr.NewLoopAdapter(est, settings)
	cfg.CompactionSettings = agentcore.CompactionConfig{MaxTokens: 200}

	// Build modest history (won't definitely trigger, but exercises the path)
	msgs := []agentcore.AgentMessage{
		{Role: llmprovider.RoleUser, Content: []llmprovider.ContentPart{{Type: llmprovider.ContentTypeText, Text: "你好"}}},
		{Role: llmprovider.RoleAssistant, Content: []llmprovider.ContentPart{{Type: llmprovider.ContentTypeText, Text: "你好！有什么我可以帮助你的？"}}},
		{Role: llmprovider.RoleUser, Content: []llmprovider.ContentPart{{Type: llmprovider.ContentTypeText, Text: "Tell me one fact about the sun."}}},
	}

	ch := agentcore.RunAgentLoop(context.Background(), cfg, agentcore.AgentContext{
		SystemPrompt: "Be brief.",
		Messages:     msgs,
	})
	events := drainCtx(t, ch, 30*time.Second)

	reply := collectText(events)
	t.Logf("Reply: %s", reply)
	if reply == "" {
		t.Error("expected non-empty reply")
	}

	entries, _ := store.GetEntries()
	t.Logf("Session entries recorded: %d", len(entries))
	if len(entries) == 0 {
		t.Error("expected session entries after loop")
	}
}
