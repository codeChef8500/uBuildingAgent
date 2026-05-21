package contextmgr

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/ubuildingagent/backend/agentcore"
	"github.com/ubuildingagent/backend/llmprovider"
)

// DefaultCompactor compacts messages using the head/tail protection strategy
// combined with an optional LLM-based summarisation step.
type DefaultCompactor struct {
	estimator *CharHeuristicEstimator
	streamFn  llmprovider.StreamFn
	model     llmprovider.Model
}

// NewDefaultCompactor creates a compactor.
// If streamFn is non-nil, dropped messages will be summarised by an LLM call.
func NewDefaultCompactor(streamFn llmprovider.StreamFn, model llmprovider.Model) *DefaultCompactor {
	return &DefaultCompactor{
		estimator: NewCharHeuristicEstimator(),
		streamFn:  streamFn,
		model:     model,
	}
}

// EstimateTokens delegates to the internal estimator.
func (c *DefaultCompactor) EstimateTokens(messages []agentcore.AgentMessage) ContextEstimate {
	return c.estimator.EstimateTokens(messages)
}

// ShouldCompact delegates to the internal estimator.
func (c *DefaultCompactor) ShouldCompact(est ContextEstimate, settings CompactionSettings) bool {
	return c.estimator.ShouldCompact(est, settings)
}

// Compact performs compaction with optional LLM summarisation.
// It respects HeadProtectN / TailProtectN and targets TargetRatio tokens.
func (c *DefaultCompactor) Compact(
	messages []agentcore.AgentMessage,
	settings CompactionSettings,
) ([]agentcore.AgentMessage, CompactionResult, error) {
	// Build SummarizeFn if an LLM stream is available
	if settings.SummarizeFn == nil && c.streamFn != nil {
		settings.SummarizeFn = c.buildSummarizeFn()
	}
	return c.estimator.Compact(messages, settings)
}

// Calibrate updates the correction factor from real token usage.
func (c *DefaultCompactor) Calibrate(messages []agentcore.AgentMessage, realTokens int) {
	c.estimator.Calibrate(messages, realTokens)
}

func (c *DefaultCompactor) buildSummarizeFn() func([]agentcore.AgentMessage) (agentcore.AgentMessage, error) {
	return func(messages []agentcore.AgentMessage) (agentcore.AgentMessage, error) {
		if c.streamFn == nil {
			return agentcore.AgentMessage{}, fmt.Errorf("compactor: no streamFn for summarisation")
		}

		// Build a minimal context asking the LLM to summarise
		var sb strings.Builder
		for _, m := range messages {
			sb.WriteString(fmt.Sprintf("[%s]: ", m.Role))
			for _, p := range m.Content {
				if p.Type == llmprovider.ContentTypeText {
					sb.WriteString(p.Text)
				}
			}
			sb.WriteString("\n")
		}

		summaryPrompt := "Please summarise the following conversation segment concisely, preserving key facts and decisions:\n\n" + sb.String()

		conv := llmprovider.Context{
			Messages: []llmprovider.Message{
				{
					Role:    llmprovider.RoleUser,
					Content: []llmprovider.ContentPart{{Type: llmprovider.ContentTypeText, Text: summaryPrompt}},
				},
			},
		}

		streamCh := c.streamFn(context.Background(), c.model, conv, llmprovider.SimpleStreamOptions{})

		var result strings.Builder
		for ev := range streamCh {
			if ev.Type == llmprovider.StreamEventTextDelta {
				result.WriteString(ev.Delta)
			}
			if ev.Type == llmprovider.StreamEventError {
				return agentcore.AgentMessage{}, ev.Err
			}
		}

		summary := result.String()
		return agentcore.AgentMessage{
			Role:   llmprovider.RoleUser,
			Hidden: true, // summary is injected as a hidden context message
			Content: []llmprovider.ContentPart{{
				Type: llmprovider.ContentTypeText,
				Text: fmt.Sprintf("[Conversation summary — %s]: %s",
					time.Now().Format("2006-01-02"), summary),
			}},
			Source: "system",
		}, nil
	}
}
