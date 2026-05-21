package contextmgr

import (
	"fmt"

	"github.com/ubuildingagent/backend/agentcore"
	"github.com/ubuildingagent/backend/llmprovider"
)

// maxToolResultChars is the maximum characters kept in a single tool result
// before Stage 1 truncation kicks in.
const maxToolResultChars = 8000

// CompactionPipeline applies multiple ContextEngine stages in sequence.
// Stages are applied only when their ShouldCompact check passes.
type CompactionPipeline struct {
	stages   []ContextEngine
	settings CompactionSettings
}

// NewCompactionPipeline builds the standard 3-stage pipeline:
//
//	Stage 1: Tool result truncation (no LLM call required)
//	Stage 2: DefaultCompactor summarisation (optional LLM call)
//	Stage 3: Emergency tail truncation (fallback)
func NewCompactionPipeline(
	streamFn llmprovider.StreamFn,
	model llmprovider.Model,
	settings CompactionSettings,
) *CompactionPipeline {
	return &CompactionPipeline{
		settings: settings,
		stages: []ContextEngine{
			&ToolResultTruncator{MaxChars: maxToolResultChars},
			NewDefaultCompactor(streamFn, model),
			&EmergencyTruncator{},
		},
	}
}

// EstimateTokens returns the estimate from the first stage.
func (p *CompactionPipeline) EstimateTokens(messages []agentcore.AgentMessage) ContextEstimate {
	return p.stages[0].EstimateTokens(messages)
}

// ShouldCompact returns true if any stage would trigger compaction.
func (p *CompactionPipeline) ShouldCompact(est ContextEstimate, settings CompactionSettings) bool {
	for _, s := range p.stages {
		if s.ShouldCompact(est, settings) {
			return true
		}
	}
	return false
}

// Compact runs stages in order until the token estimate is below the target.
func (p *CompactionPipeline) Compact(
	messages []agentcore.AgentMessage,
	settings CompactionSettings,
) ([]agentcore.AgentMessage, CompactionResult, error) {
	current := messages
	var last CompactionResult

	for i, stage := range p.stages {
		est := stage.EstimateTokens(current)
		if !stage.ShouldCompact(est, settings) {
			continue
		}
		compacted, result, err := stage.Compact(current, settings)
		if err != nil {
			return current, last, fmt.Errorf("pipeline stage %d: %w", i, err)
		}
		current = compacted
		last = result

		// Re-check after this stage
		postEst := stage.EstimateTokens(current)
		if !stage.ShouldCompact(postEst, settings) {
			break
		}
	}
	return current, last, nil
}

// ── Stage 1: Tool result truncator ────────────────────────────────────────

// ToolResultTruncator truncates overlong tool result content parts.
type ToolResultTruncator struct {
	MaxChars int
}

func (t *ToolResultTruncator) EstimateTokens(messages []agentcore.AgentMessage) ContextEstimate {
	return NewCharHeuristicEstimator().EstimateTokens(messages)
}

func (t *ToolResultTruncator) ShouldCompact(est ContextEstimate, settings CompactionSettings) bool {
	return est.TotalTokens > settings.MaxTokens/4
}

func (t *ToolResultTruncator) Compact(
	messages []agentcore.AgentMessage,
	settings CompactionSettings,
) ([]agentcore.AgentMessage, CompactionResult, error) {
	max := t.MaxChars
	if max <= 0 {
		max = maxToolResultChars
	}
	pre := NewCharHeuristicEstimator().EstimateTokens(messages).TotalTokens

	result := make([]agentcore.AgentMessage, len(messages))
	copy(result, messages)

	for i, msg := range result {
		if msg.Role != llmprovider.RoleTool {
			continue
		}
		updated := make([]llmprovider.ContentPart, len(msg.Content))
		copy(updated, msg.Content)
		for j, p := range updated {
			if p.Type == llmprovider.ContentTypeToolResult && len(p.ToolResult) > max {
				truncated := p.ToolResult[:max]
				updated[j].ToolResult = truncated + fmt.Sprintf("\n...[truncated %d chars]", len(p.ToolResult)-max)
			}
		}
		result[i].Content = updated
	}

	post := NewCharHeuristicEstimator().EstimateTokens(result).TotalTokens
	return result, CompactionResult{PreTokens: pre, PostTokens: post}, nil
}

// ── Stage 3: Emergency truncator ──────────────────────────────────────────

// EmergencyTruncator removes messages from the middle as a last resort,
// keeping head+tail protected messages.
type EmergencyTruncator struct{}

func (e *EmergencyTruncator) EstimateTokens(messages []agentcore.AgentMessage) ContextEstimate {
	return NewCharHeuristicEstimator().EstimateTokens(messages)
}

func (e *EmergencyTruncator) ShouldCompact(est ContextEstimate, settings CompactionSettings) bool {
	if settings.MaxTokens <= 0 {
		return false
	}
	return est.TotalTokens > settings.MaxTokens
}

func (e *EmergencyTruncator) Compact(
	messages []agentcore.AgentMessage,
	settings CompactionSettings,
) ([]agentcore.AgentMessage, CompactionResult, error) {
	pre := e.EstimateTokens(messages).TotalTokens

	head := settings.HeadProtectN
	tail := settings.TailProtectN
	if head < 0 {
		head = 0
	}
	if tail < 0 {
		tail = 0
	}

	target := settings.MaxTokens
	if target <= 0 {
		return messages, CompactionResult{PreTokens: pre, PostTokens: pre}, nil
	}

	est := NewCharHeuristicEstimator()
	result := make([]agentcore.AgentMessage, 0, len(messages))
	result = append(result, messages[:head]...)

	// Keep only tail messages from the middle+tail
	midAndTail := messages[head:]
	kept := keepNewest(midAndTail, target-est.EstimateTokens(messages[:head]).TotalTokens, est, 1.0)
	result = append(result, kept...)

	post := est.EstimateTokens(result).TotalTokens
	return result, CompactionResult{
		PreTokens:      pre,
		PostTokens:     post,
		CompactedCount: len(messages) - len(result),
	}, nil
}
