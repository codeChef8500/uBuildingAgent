package agentcore_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/ubuildingagent/backend/agentcore"
	"github.com/ubuildingagent/backend/agentcore/contextmgr"
	"github.com/ubuildingagent/backend/agentcore/session"
	"github.com/ubuildingagent/backend/llmprovider"
	_ "github.com/ubuildingagent/backend/llmprovider/providers"
)

// ── Self-contained mock (external package; cannot use loop_test.go internals) ──

type extTurn struct {
	text      string
	toolCalls []llmprovider.ToolCall
}

type extMock struct {
	turns   []extTurn
	callIdx int
}

func (m *extMock) ApiType() llmprovider.ApiType { return llmprovider.ApiOpenAICompletions }

func (m *extMock) Stream(_ context.Context, _ llmprovider.Model, _ llmprovider.Context, _ llmprovider.StreamOptions) <-chan llmprovider.StreamEvent {
	return m.emit()
}

func (m *extMock) StreamSimple(_ context.Context, _ llmprovider.Model, _ llmprovider.Context, _ llmprovider.SimpleStreamOptions) <-chan llmprovider.StreamEvent {
	return m.emit()
}

func (m *extMock) emit() <-chan llmprovider.StreamEvent {
	ch := make(chan llmprovider.StreamEvent, 16)
	idx := m.callIdx
	if idx >= len(m.turns) {
		idx = len(m.turns) - 1
	}
	m.callIdx++
	turn := m.turns[idx]
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
		ch <- llmprovider.StreamEvent{Type: llmprovider.StreamEventMessageEnd, Usage: &llmprovider.Usage{}}
	}()
	return ch
}

func withMock(turns ...extTurn) (*extMock, func()) {
	m := &extMock{turns: turns}
	llmprovider.Register(m, "ext-mock-cs")
	return m, func() { llmprovider.Unregister("ext-mock-cs") }
}

func extModel() llmprovider.Model {
	return llmprovider.Model{ID: "mock", Api: llmprovider.ApiOpenAICompletions}
}

func userMsg(text string) agentcore.AgentMessage {
	return agentcore.AgentMessage{
		Role:    llmprovider.RoleUser,
		Content: []llmprovider.ContentPart{{Type: llmprovider.ContentTypeText, Text: text}},
	}
}

// ── T1: Session records assistant messages ─────────────────────────────────

func TestLoop_SessionRecordsMessages(t *testing.T) {
	_, cleanup := withMock(extTurn{text: "reply"})
	defer cleanup()

	store := session.NewInMemoryStorage("s1")
	writer := session.NewLoopWriter(store, "s1")

	cfg := agentcore.AgentLoopConfig{Model: extModel(), Session: writer}
	conv := agentcore.AgentContext{Messages: []agentcore.AgentMessage{userMsg("hi")}}

	for range agentcore.RunAgentLoop(context.Background(), cfg, conv) {
	}

	entries, _ := store.GetEntries()
	if len(entries) == 0 {
		t.Fatal("expected session entries")
	}
	for _, e := range entries {
		if e.Type != session.EntryTypeMessage {
			t.Errorf("unexpected entry type %q", e.Type)
		}
	}
	t.Logf("Session recorded %d message entries", len(entries))
}

// ── T2: Session records tool results ───────────────────────────────────────

func TestLoop_SessionRecordsToolResults(t *testing.T) {
	tc := llmprovider.ToolCall{ID: "c1", Name: "my_tool", Arguments: json.RawMessage(`{}`)}
	_, cleanup := withMock(
		extTurn{toolCalls: []llmprovider.ToolCall{tc}},
		extTurn{text: "done"},
	)
	defer cleanup()

	store := session.NewInMemoryStorage("s2")
	writer := session.NewLoopWriter(store, "s2")

	cfg := agentcore.AgentLoopConfig{Model: extModel(), Session: writer}
	conv := agentcore.AgentContext{
		Messages: []agentcore.AgentMessage{userMsg("go")},
		Tools: []agentcore.AgentTool{
			{Name: "my_tool", Execute: func(ctx *agentcore.ToolExecContext) agentcore.AgentToolResult {
				return agentcore.AgentToolResult{Content: "tool_output"}
			}},
		},
	}

	for range agentcore.RunAgentLoop(context.Background(), cfg, conv) {
	}

	entries, _ := store.GetEntries()
	typeCount := map[session.EntryType]int{}
	for _, e := range entries {
		typeCount[e.Type]++
	}
	t.Logf("Entry types: %v", typeCount)

	if typeCount["message"] == 0 {
		t.Error("expected message entries")
	}
	if typeCount["tool_result"] == 0 {
		t.Error("expected tool_result entries")
	}
}

// ── T3: Session restore → continue conversation ───────────────────────────

func TestLoop_SessionRestore(t *testing.T) {
	_, cleanup := withMock(
		extTurn{text: "first reply"},
		extTurn{text: "second reply"},
	)
	defer cleanup()

	store := session.NewInMemoryStorage("s3")
	writer := session.NewLoopWriter(store, "s3")

	cfg := agentcore.AgentLoopConfig{Model: extModel(), Session: writer}

	// First run
	for range agentcore.RunAgentLoop(context.Background(), cfg,
		agentcore.AgentContext{Messages: []agentcore.AgentMessage{userMsg("turn1")}}) {
	}
	after1, _ := store.GetEntries()

	// Restore from session
	restored := restoreFromSession(t, store)
	t.Logf("Restored %d messages from session", len(restored))

	// Second run
	restored = append(restored, userMsg("turn2"))
	for range agentcore.RunAgentLoop(context.Background(), cfg,
		agentcore.AgentContext{Messages: restored}) {
	}
	after2, _ := store.GetEntries()

	if len(after2) <= len(after1) {
		t.Errorf("expected more entries after second run: before=%d after=%d", len(after1), len(after2))
	}
	t.Logf("Entries: turn1=%d → turn2=%d", len(after1), len(after2))
}

func restoreFromSession(t *testing.T, store session.SessionStorage) []agentcore.AgentMessage {
	t.Helper()
	entries, _ := store.GetEntries()
	var msgs []agentcore.AgentMessage
	for _, e := range entries {
		if e.Type != session.EntryTypeMessage {
			continue
		}
		var payload map[string]json.RawMessage
		if err := json.Unmarshal(e.Payload, &payload); err != nil {
			continue
		}
		raw, ok := payload["message"]
		if !ok {
			continue
		}
		var msg agentcore.AgentMessage
		if json.Unmarshal(raw, &msg) == nil {
			msgs = append(msgs, msg)
		}
	}
	return msgs
}

// ── T4: Context compaction triggers via mock engine ───────────────────────

func TestLoop_ContextCompactionTriggers(t *testing.T) {
	_, cleanup := withMock(extTurn{text: "reply after compact"})
	defer cleanup()

	compactCalled := false
	engine := &mockEngine{
		estimateFn: func([]agentcore.AgentMessage) int { return 9999 },
		shouldFn:   func(int, int) bool { return true },
		compactFn: func(msgs []agentcore.AgentMessage, _ int) ([]agentcore.AgentMessage, error) {
			compactCalled = true
			if len(msgs) > 1 {
				return msgs[len(msgs)-1:], nil
			}
			return msgs, nil
		},
	}

	cfg := agentcore.AgentLoopConfig{
		Model:              extModel(),
		ContextEngine:      engine,
		CompactionSettings: agentcore.CompactionConfig{MaxTokens: 100},
	}
	conv := agentcore.AgentContext{Messages: []agentcore.AgentMessage{
		userMsg("m1"), userMsg("m2"), userMsg("m3"),
	}}

	for ev := range agentcore.RunAgentLoop(context.Background(), cfg, conv) {
		if ev.Type == agentcore.AgentEventError {
			t.Fatalf("loop error: %v", ev.Err)
		}
	}

	if !compactCalled {
		t.Error("expected CompactMessages to be called")
	}
}

// ── T5: DefaultCompactor adapter integrates with loop ─────────────────────

func TestLoop_CompactionAdapterWiring(t *testing.T) {
	_, cleanup := withMock(extTurn{text: "ok"})
	defer cleanup()

	msgs := make([]agentcore.AgentMessage, 30)
	for i := range msgs {
		msgs[i] = userMsg("this message has enough words to produce meaningful token counts")
	}

	est := contextmgr.NewCharHeuristicEstimator()
	settings := contextmgr.CompactionSettings{
		MaxTokens: 50, HeadProtectN: 1, TailProtectN: 1, TargetRatio: 0.5,
	}
	adapter := contextmgr.NewLoopAdapter(est, settings)

	cfg := agentcore.AgentLoopConfig{
		Model:              extModel(),
		ContextEngine:      adapter,
		CompactionSettings: agentcore.CompactionConfig{MaxTokens: 50},
	}

	for ev := range agentcore.RunAgentLoop(context.Background(), cfg, agentcore.AgentContext{Messages: msgs}) {
		if ev.Type == agentcore.AgentEventError {
			t.Fatalf("loop error: %v", ev.Err)
		}
	}
}

// ── T6: Compaction + session together ─────────────────────────────────────

func TestLoop_CompactionAndSessionTogether(t *testing.T) {
	_, cleanup := withMock(extTurn{text: "ok"})
	defer cleanup()

	store := session.NewInMemoryStorage("s6")
	writer := session.NewLoopWriter(store, "s6")

	est := contextmgr.NewCharHeuristicEstimator()
	settings := contextmgr.CompactionSettings{MaxTokens: 30, HeadProtectN: 1, TailProtectN: 1}
	adapter := contextmgr.NewLoopAdapter(est, settings)

	cfg := agentcore.AgentLoopConfig{
		Model:              extModel(),
		Session:            writer,
		ContextEngine:      adapter,
		CompactionSettings: agentcore.CompactionConfig{MaxTokens: 30},
	}

	msgs := make([]agentcore.AgentMessage, 20)
	for i := range msgs {
		msgs[i] = userMsg("padding message content here for token counting")
	}

	for ev := range agentcore.RunAgentLoop(context.Background(), cfg, agentcore.AgentContext{Messages: msgs}) {
		if ev.Type == agentcore.AgentEventError {
			t.Fatalf("loop error: %v", ev.Err)
		}
	}

	entries, _ := store.GetEntries()
	typeCount := map[session.EntryType]int{}
	for _, e := range entries {
		typeCount[e.Type]++
	}
	t.Logf("Entries: %v", typeCount)

	if typeCount["message"] == 0 {
		t.Error("expected message entries in session")
	}
	if typeCount["compaction"] == 0 {
		t.Error("expected compaction entries in session")
	}
}

// ── Mock ContextEngineIface ────────────────────────────────────────────────

type mockEngine struct {
	estimateFn func([]agentcore.AgentMessage) int
	shouldFn   func(int, int) bool
	compactFn  func([]agentcore.AgentMessage, int) ([]agentcore.AgentMessage, error)
}

func (m *mockEngine) EstimateContextTokens(msgs []agentcore.AgentMessage) int {
	if m.estimateFn != nil {
		return m.estimateFn(msgs)
	}
	return 0
}
func (m *mockEngine) ShouldCompact(tokens, max int) bool {
	if m.shouldFn != nil {
		return m.shouldFn(tokens, max)
	}
	return false
}
func (m *mockEngine) CompactMessages(msgs []agentcore.AgentMessage, max int) ([]agentcore.AgentMessage, error) {
	if m.compactFn != nil {
		return m.compactFn(msgs, max)
	}
	return msgs, nil
}
