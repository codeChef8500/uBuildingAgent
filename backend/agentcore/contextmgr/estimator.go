package contextmgr

import (
	"encoding/json"
	"strings"

	"github.com/ubuildingagent/backend/agentcore"
	"github.com/ubuildingagent/backend/llmprovider"
)

// charsPerToken is the heuristic conversion ratio: ~4 chars per token for English text.
const charsPerToken = 4

// CharHeuristicEstimator estimates tokens using the "4 chars per token" heuristic.
// It can be calibrated with a usage correction factor from actual LLM responses.
type CharHeuristicEstimator struct {
	// CorrectionFactor allows calibration against real usage (default 1.0).
	// Set to realTokens / estimatedTokens after a real API call.
	CorrectionFactor float64
}

// NewCharHeuristicEstimator returns an uncalibrated estimator.
func NewCharHeuristicEstimator() *CharHeuristicEstimator {
	return &CharHeuristicEstimator{CorrectionFactor: 1.0}
}

// EstimateTokens estimates tokens for each message using character count heuristics.
func (e *CharHeuristicEstimator) EstimateTokens(messages []agentcore.AgentMessage) ContextEstimate {
	cf := e.CorrectionFactor
	if cf <= 0 {
		cf = 1.0
	}

	total := 0
	breakdown := make([]MessageTokenBreakdown, 0, len(messages))

	for i, msg := range messages {
		chars := messageCharCount(msg)
		tokens := int(float64(chars)/charsPerToken*cf) + 4 // +4 overhead per message
		total += tokens
		breakdown = append(breakdown, MessageTokenBreakdown{
			Index:  i,
			Role:   msg.Role,
			Tokens: tokens,
		})
	}

	return ContextEstimate{
		TotalTokens:      total,
		MessageBreakdown: breakdown,
	}
}

// ShouldCompact returns true if TotalTokens exceeds 80% of MaxTokens.
func (e *CharHeuristicEstimator) ShouldCompact(est ContextEstimate, settings CompactionSettings) bool {
	if settings.MaxTokens <= 0 {
		return false
	}
	threshold := int(float64(settings.MaxTokens) * 0.8)
	return est.TotalTokens >= threshold
}

// Compact performs a simple head+tail preserve / middle truncate without summarisation.
func (e *CharHeuristicEstimator) Compact(
	messages []agentcore.AgentMessage,
	settings CompactionSettings,
) ([]agentcore.AgentMessage, CompactionResult, error) {
	estimate := e.EstimateTokens(messages)
	pre := estimate.TotalTokens

	head := settings.HeadProtectN
	tail := settings.TailProtectN
	if head < 0 {
		head = 0
	}
	if tail < 0 {
		tail = 0
	}
	if head+tail >= len(messages) {
		// Nothing to compact
		return messages, CompactionResult{PreTokens: pre, PostTokens: pre}, nil
	}

	compactable := messages[head : len(messages)-tail]

	targetTokens := int(float64(settings.MaxTokens) * settings.TargetRatio)
	if settings.TargetRatio <= 0 {
		targetTokens = settings.MaxTokens / 2
	}
	// Budget available for the compactable window
	headTokens := e.EstimateTokens(messages[:head]).TotalTokens
	tailTokens := e.EstimateTokens(messages[len(messages)-tail:]).TotalTokens
	availableForMiddle := targetTokens - headTokens - tailTokens
	if availableForMiddle <= 0 {
		availableForMiddle = 0
	}

	kept := keepNewest(compactable, availableForMiddle, e, cf(e))

	result := make([]agentcore.AgentMessage, 0, head+len(kept)+tail)
	result = append(result, messages[:head]...)

	var summary *agentcore.AgentMessage
	if settings.SummarizeFn != nil && len(kept) < len(compactable) {
		dropped := messages[head : head+(len(compactable)-len(kept))]
		sumMsg, err := settings.SummarizeFn(dropped)
		if err == nil {
			result = append(result, sumMsg)
			summary = &sumMsg
		}
	}

	result = append(result, kept...)
	result = append(result, messages[len(messages)-tail:]...)

	postEst := e.EstimateTokens(result)
	return result, CompactionResult{
		PreTokens:      pre,
		PostTokens:     postEst.TotalTokens,
		SummaryMessage: summary,
		CompactedCount: len(compactable) - len(kept),
	}, nil
}

// keepNewest keeps messages from the end of the slice until the token budget is spent.
func keepNewest(msgs []agentcore.AgentMessage, budget int, e *CharHeuristicEstimator, correctionFactor float64) []agentcore.AgentMessage {
	kept := []agentcore.AgentMessage{}
	used := 0
	for i := len(msgs) - 1; i >= 0; i-- {
		chars := messageCharCount(msgs[i])
		t := int(float64(chars)/charsPerToken*correctionFactor) + 4
		if used+t > budget && len(kept) > 0 {
			break
		}
		kept = append([]agentcore.AgentMessage{msgs[i]}, kept...)
		used += t
	}
	return kept
}

func cf(e *CharHeuristicEstimator) float64 {
	if e.CorrectionFactor <= 0 {
		return 1.0
	}
	return e.CorrectionFactor
}

// messageCharCount counts characters across all content parts and tool calls.
func messageCharCount(msg agentcore.AgentMessage) int {
	count := 0
	for _, p := range msg.Content {
		switch p.Type {
		case llmprovider.ContentTypeText:
			count += len(p.Text)
		case llmprovider.ContentTypeToolResult:
			count += len(p.ToolResult)
		}
	}
	for _, tc := range msg.ToolCalls {
		count += len(tc.Name)
		if len(tc.Arguments) > 0 {
			count += len(tc.Arguments)
		}
	}
	count += len(msg.Thinking)
	return count
}

// Calibrate updates the correction factor based on a real usage observation.
// Call this after receiving a StreamEventMessageEnd with actual token counts.
func (e *CharHeuristicEstimator) Calibrate(messages []agentcore.AgentMessage, realTokens int) {
	if realTokens <= 0 {
		return
	}
	est := e.EstimateTokens(messages)
	if est.TotalTokens == 0 {
		return
	}
	e.CorrectionFactor = float64(realTokens) / float64(est.TotalTokens) * e.CorrectionFactor
}

// messageToJSON is a utility for compactors that need to serialize messages.
func messageToJSON(msg agentcore.AgentMessage) string {
	data, err := json.Marshal(msg)
	if err != nil {
		return strings.Join(extractTexts(msg), " ")
	}
	return string(data)
}

func extractTexts(msg agentcore.AgentMessage) []string {
	var parts []string
	for _, p := range msg.Content {
		if p.Type == llmprovider.ContentTypeText {
			parts = append(parts, p.Text)
		}
	}
	return parts
}
