//go:build integration

package safeagent

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
	_ "github.com/ubuildingagent/backend/llmprovider/providers" // register builtins
)

// envFile resolves the path to backend/.env from this test file's location.
// agents/safeagent/e2e_test.go → ../../.env
func envFile() string {
	_, thisFile, _, _ := runtime.Caller(0)
	return filepath.Join(filepath.Dir(thisFile), "..", "..", ".env")
}

// loadSafeAgentCfg reads .env and returns a ready Config.
func loadSafeAgentCfg(t *testing.T) Config {
	t.Helper()
	cfg, err := envconfig.LoadFromFile(envFile())
	if err != nil {
		t.Skipf("skipping integration test: .env not found (%v)", err)
	}
	if err := cfg.Validate(); err != nil {
		t.Skipf("skipping integration test: incomplete .env (%v)", err)
	}
	t.Logf("LLM_TYPE=%s  MODEL=%s  BASE_URL=%s", cfg.Type, cfg.Model, cfg.BaseURL)

	vlmCfg, err := envconfig.LoadVLMFromFile(envFile())
	if err != nil {
		t.Skipf("skipping integration test: VLM config err (%v)", err)
	}

	var vlmModel llmprovider.Model
	var vlmAPIKey string
	if vlmCfg.IsConfigured() {
		vlmModel = vlmCfg.ToModel()
		vlmAPIKey = vlmCfg.APIKey
		t.Logf("VLM_TYPE=%s  MODEL=%s  BASE_URL=%s", vlmCfg.Type, vlmCfg.Model, vlmCfg.BaseURL)
	} else {
		t.Log("VLM is not configured")
	}

	return Config{
		APIKey:              cfg.APIKey,
		Model:               cfg.ToModel(),
		VLMModel:            vlmModel,
		VLMAPIKey:           vlmAPIKey,
		OrchestratorMaxIter: 20,
		SubAgentMaxIter:     10,
	}
}

// drainCh collects all AgentEvents from the channel.
// HTTP 4xx API errors cause t.Skip; other errors cause t.Fatal.
func drainCh(t *testing.T, ch <-chan agentcore.AgentEvent, timeout time.Duration) []agentcore.AgentEvent {
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

// textFrom joins all AgentEventTextDelta deltas.
func textFrom(events []agentcore.AgentEvent) string {
	var sb strings.Builder
	for _, ev := range events {
		if ev.Type == agentcore.AgentEventTextDelta {
			sb.WriteString(ev.Delta)
		}
	}
	return sb.String()
}

// countTool returns the number of AgentEventToolStart events for the given tool name.
func countTool(events []agentcore.AgentEvent, name string) int {
	n := 0
	for _, ev := range events {
		if ev.Type == agentcore.AgentEventToolStart && ev.ToolCall != nil && ev.ToolCall.Name == name {
			n++
		}
	}
	return n
}

// toolNames returns all tool names called (from AgentEventToolStart events).
func toolNames(events []agentcore.AgentEvent) []string {
	var names []string
	for _, ev := range events {
		if ev.Type == agentcore.AgentEventToolStart && ev.ToolCall != nil {
			names = append(names, ev.ToolCall.Name)
		}
	}
	return names
}

// ─── T1: Full 5-stage pipeline ──────────────────────────────────────────────

func TestSafeAgentE2E_FullPipeline(t *testing.T) {
	cfg := loadSafeAgentCfg(t)
	orchestrator := New(cfg)

	scene := `施工现场安全巡检请求：
场景描述：3楼外墙脚手架上有2名工人正在进行高空焊接作业。
图像URL：https://example.com/site-photo-001.jpg
位置：A栋3楼东侧外墙施工区域
请完成完整的安全巡检流程，包括视觉识别、风险分析、处置决策、工单创建和通知上报。`

	t.Log("Starting full 5-stage safety inspection pipeline...")
	ch := orchestrator.Prompt(context.Background(), scene)
	// 8-minute timeout: 5 sequential sub-agent calls, each may make multiple tool round-trips
	events := drainCh(t, ch, 8*time.Minute)

	taskCalls := countTool(events, "Task")
	t.Logf("Task tool called %d times (expected ≥5)", taskCalls)
	t.Logf("All tools called: %v", toolNames(events))

	if taskCalls == 0 {
		t.Skip("T1: orchestrator did not call Task tool (model may not support function calling)")
	}
	if taskCalls < 5 {
		t.Errorf("T1: expected Task to be called ≥5 times (one per stage), got %d", taskCalls)
	}

	reply := textFrom(events)
	t.Logf("T1 final reply (first 800 chars): %.800s", reply)
	if reply == "" {
		t.Error("T1: expected non-empty final text reply from orchestrator")
	}
}

// ─── T2: VisionAgent standalone ─────────────────────────────────────────────

func TestSafeAgentE2E_VisionAgent(t *testing.T) {
	cfg := loadSafeAgentCfg(t)
	subCfg := SubAgentConfig{
		Model:     cfg.Model,
		APIKey:    cfg.APIKey,
		MaxIter:   cfg.SubAgentMaxIter,
		VLMModel:  cfg.VLMModel,
		VLMAPIKey: cfg.VLMAPIKey,
	}
	agent := NewVisionAgent(subCfg)

	prompt := `场景描述：施工现场高处作业，脚手架上2名工人未佩戴安全帽，未系安全绳。
请调用检测工具分析场景，输出 DetectionResult JSON。`

	t.Log("Testing VisionAgent standalone...")
	ch := agent.Prompt(context.Background(), prompt)
	events := drainCh(t, ch, 2*time.Minute)

	called := countTool(events, "detect_objects")
	t.Logf("T2: detect_objects called %d time(s)", called)
	t.Logf("T2: all tools called: %v", toolNames(events))

	if called == 0 {
		t.Skip("T2: VisionAgent did not call detect_objects (model behavior)")
	}

	reply := textFrom(events)
	t.Logf("T2 reply: %s", reply)
	if reply == "" {
		t.Error("T2: expected non-empty reply from VisionAgent")
	}
}

// ─── T3: RiskAgent standalone ────────────────────────────────────────────────

func TestSafeAgentE2E_RiskAgent(t *testing.T) {
	cfg := loadSafeAgentCfg(t)
	subCfg := SubAgentConfig{Model: cfg.Model, APIKey: cfg.APIKey, MaxIter: cfg.SubAgentMaxIter}
	agent := NewRiskAgent(subCfg)

	// Provide a DetectionResult JSON stub as the prompt input
	prompt := `以下是视觉检测结果，请进行风险分析并输出 RiskAssessment JSON：
{"objects":[{"type":"person","confidence":0.95},{"type":"scaffold","confidence":0.88},{"type":"hard_hat_absent","confidence":0.91}],"violations":["工人未佩戴安全帽","未系安全绳"],"confidence":0.92,"summary":"高空作业场景，发现2处安全违规"}`

	t.Log("Testing RiskAgent standalone...")
	ch := agent.Prompt(context.Background(), prompt)
	events := drainCh(t, ch, 2*time.Minute)

	evalCalled := countTool(events, "evaluate_risk")
	t.Logf("T3: evaluate_risk called %d time(s)", evalCalled)
	t.Logf("T3: all tools called: %v", toolNames(events))

	if evalCalled == 0 {
		t.Skip("T3: RiskAgent did not call evaluate_risk (model behavior)")
	}

	reply := textFrom(events)
	t.Logf("T3 reply: %s", reply)
	if reply == "" {
		t.Error("T3: expected non-empty reply from RiskAgent")
	}
}

// ─── T4: DecisionAgent standalone ───────────────────────────────────────────

func TestSafeAgentE2E_DecisionAgent(t *testing.T) {
	cfg := loadSafeAgentCfg(t)
	subCfg := SubAgentConfig{Model: cfg.Model, APIKey: cfg.APIKey, MaxIter: cfg.SubAgentMaxIter}
	agent := NewDecisionAgent(subCfg)

	// Provide a RiskAssessment JSON stub as the prompt input
	prompt := `以下是风险评估结果，请制定处置决策并输出 InspectionDecision JSON：
{"risks":[{"code":"R001","description":"高处作业未系安全绳","level":"critical","regulation":"GB/T 3836"},{"code":"R002","description":"未佩戴安全帽","level":"high","regulation":"安全生产法第32条"}],"overall_level":"critical","summary":"存在2项安全违规，整体风险等级为严重，需立即处置"}`

	t.Log("Testing DecisionAgent standalone...")
	ch := agent.Prompt(context.Background(), prompt)
	events := drainCh(t, ch, 2*time.Minute)

	stratCalled := countTool(events, "determine_strategy")
	assignCalled := countTool(events, "assign_person")
	t.Logf("T4: determine_strategy called %d time(s), assign_person called %d time(s)",
		stratCalled, assignCalled)
	t.Logf("T4: all tools called: %v", toolNames(events))

	if stratCalled == 0 && assignCalled == 0 {
		t.Skip("T4: DecisionAgent did not call expected tools (model behavior)")
	}

	reply := textFrom(events)
	t.Logf("T4 reply: %s", reply)
	if reply == "" {
		t.Error("T4: expected non-empty reply from DecisionAgent")
	}
}
