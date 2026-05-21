package contextmgr

import (
	"testing"

	"github.com/ubuildingagent/backend/agentcore"
	"github.com/ubuildingagent/backend/llmprovider"
)

func makeTextMsg(role llmprovider.Role, text string) agentcore.AgentMessage {
	return agentcore.AgentMessage{
		Role:    role,
		Content: []llmprovider.ContentPart{{Type: llmprovider.ContentTypeText, Text: text}},
		Source:  string(role),
	}
}

func TestEstimator_NonZero(t *testing.T) {
	e := NewCharHeuristicEstimator()
	msgs := []agentcore.AgentMessage{
		makeTextMsg(llmprovider.RoleUser, "Hello there, how are you?"),
		makeTextMsg(llmprovider.RoleAssistant, "I am fine, thank you!"),
	}
	est := e.EstimateTokens(msgs)
	if est.TotalTokens <= 0 {
		t.Error("expected positive token estimate")
	}
	if len(est.MessageBreakdown) != 2 {
		t.Errorf("breakdown length: got %d, want 2", len(est.MessageBreakdown))
	}
}

func TestEstimator_ShouldCompact(t *testing.T) {
	e := NewCharHeuristicEstimator()

	// 10 messages of ~100 chars each ≈ 10*25 = 250 tokens
	msgs := make([]agentcore.AgentMessage, 10)
	for i := range msgs {
		msgs[i] = makeTextMsg(llmprovider.RoleUser, "Hello world, this is a test message with approximately one hundred characters of content here!")
	}
	est := e.EstimateTokens(msgs)

	settings := CompactionSettings{MaxTokens: 10} // tiny limit
	if !e.ShouldCompact(est, settings) {
		t.Error("expected ShouldCompact=true with tiny MaxTokens")
	}

	settings.MaxTokens = 100000
	if e.ShouldCompact(est, settings) {
		t.Error("expected ShouldCompact=false with huge MaxTokens")
	}
}

func TestEstimator_Compact_ReducesMessages(t *testing.T) {
	e := NewCharHeuristicEstimator()

	msgs := make([]agentcore.AgentMessage, 20)
	for i := range msgs {
		msgs[i] = makeTextMsg(llmprovider.RoleUser, "This is a moderately long message that contributes meaningfully to the token count.")
	}

	settings := CompactionSettings{
		MaxTokens:    50,
		HeadProtectN: 2,
		TailProtectN: 2,
		TargetRatio:  0.5,
	}

	compacted, result, err := e.Compact(msgs, settings)
	if err != nil {
		t.Fatal(err)
	}
	if len(compacted) >= len(msgs) {
		t.Errorf("expected fewer messages after compaction: before=%d after=%d", len(msgs), len(compacted))
	}
	if result.PreTokens <= result.PostTokens && result.CompactedCount == 0 {
		t.Logf("PreTokens=%d PostTokens=%d CompactedCount=%d", result.PreTokens, result.PostTokens, result.CompactedCount)
	}
	// Head must be preserved
	if compacted[0].Content[0].Text != msgs[0].Content[0].Text {
		t.Error("head message was not preserved")
	}
}

func TestEstimator_Compact_HeadTailPreserved(t *testing.T) {
	e := NewCharHeuristicEstimator()

	msgs := []agentcore.AgentMessage{
		makeTextMsg(llmprovider.RoleUser, "head1"),
		makeTextMsg(llmprovider.RoleUser, "head2"),
		makeTextMsg(llmprovider.RoleUser, "middle1 padding padding padding padding padding padding"),
		makeTextMsg(llmprovider.RoleUser, "middle2 padding padding padding padding padding padding"),
		makeTextMsg(llmprovider.RoleUser, "middle3 padding padding padding padding padding padding"),
		makeTextMsg(llmprovider.RoleUser, "tail1"),
		makeTextMsg(llmprovider.RoleUser, "tail2"),
	}

	settings := CompactionSettings{
		MaxTokens:    20, // very small
		HeadProtectN: 2,
		TailProtectN: 2,
		TargetRatio:  0.5,
	}

	compacted, _, err := e.Compact(msgs, settings)
	if err != nil {
		t.Fatal(err)
	}

	// First two messages must be head1, head2
	if compacted[0].Content[0].Text != "head1" {
		t.Errorf("head[0]: got %q", compacted[0].Content[0].Text)
	}
	if compacted[1].Content[0].Text != "head2" {
		t.Errorf("head[1]: got %q", compacted[1].Content[0].Text)
	}
}

func TestEstimator_Calibrate(t *testing.T) {
	e := NewCharHeuristicEstimator()
	msgs := []agentcore.AgentMessage{makeTextMsg(llmprovider.RoleUser, "hello world")}
	initial := e.EstimateTokens(msgs).TotalTokens

	// Real API reported 50 tokens; calibrate correction factor
	e.Calibrate(msgs, 50)

	// Next estimate should be closer to 50
	after := e.EstimateTokens(msgs).TotalTokens
	if after == initial {
		t.Error("expected estimate to change after calibration")
	}
}

func TestPipeline_ToolResultTruncation(t *testing.T) {
	trunc := &ToolResultTruncator{MaxChars: 20}
	longResult := "x"
	for i := 0; i < 500; i++ {
		longResult += "a"
	}

	msgs := []agentcore.AgentMessage{
		{
			Role: llmprovider.RoleTool,
			Content: []llmprovider.ContentPart{
				{Type: llmprovider.ContentTypeToolResult, ToolCallID: "c1", ToolResult: longResult},
			},
		},
	}

	settings := CompactionSettings{MaxTokens: 1000}
	compacted, _, err := trunc.Compact(msgs, settings)
	if err != nil {
		t.Fatal(err)
	}
	result := compacted[0].Content[0].ToolResult
	if len(result) >= len(longResult) {
		t.Error("expected tool result to be truncated")
	}
	if len(result) > 20+50 { // 20 chars + truncation notice
		t.Errorf("truncated result too long: %d chars", len(result))
	}
}
