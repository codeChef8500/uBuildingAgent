package qwen

import (
	"context"
	"encoding/json"
	"io"
	"strings"

	"github.com/ubuildingagent/backend/llmprovider"
	"github.com/ubuildingagent/backend/llmprovider/providers/sseutil"
)

// ParseDashScopeSSE reads the DashScope SSE stream and emits StreamEvents.
//
// DashScope SSE format (X-DashScope-SSE: enable):
//
//	id:1
//	event:result
//	data:{"output":{"choices":[{"delta":{"content":[{"text":"Hello"}]},"finish_reason":null}]},"usage":{...}}
func ParseDashScopeSSE(ctx context.Context, r io.Reader, ch chan<- llmprovider.StreamEvent) {
	scanner := sseutil.NewScanner(r)
	toolArgs := map[int]*strings.Builder{}
	toolMeta := map[int]llmprovider.ToolCallDelta{}

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			ch <- llmprovider.StreamEvent{Type: llmprovider.StreamEventError, Err: ctx.Err()}
			return
		default:
		}

		ev := scanner.Event()
		if ev.Data == "" {
			continue
		}

		var chunk dashScopeChunk
		if err := json.Unmarshal([]byte(ev.Data), &chunk); err != nil {
			continue
		}

		if len(chunk.Output.Choices) == 0 {
			continue
		}

		choice := chunk.Output.Choices[0]
		finishReason := choice.FinishReason

		// DashScope content is an array of typed blocks
		for _, block := range choice.Delta.Content {
			switch block.Type {
			case "", "text":
				if block.Text != "" {
					ch <- llmprovider.StreamEvent{
						Type:  llmprovider.StreamEventTextDelta,
						Delta: block.Text,
					}
				}
			case "reasoning_content":
				if block.Text != "" {
					ch <- llmprovider.StreamEvent{
						Type:  llmprovider.StreamEventThinkingDelta,
						Delta: block.Text,
					}
				}
			}
		}

		// Tool calls (same structure as OpenAI)
		for _, tc := range choice.Delta.ToolCalls {
			idx := tc.Index
			if _, ok := toolMeta[idx]; !ok {
				toolMeta[idx] = llmprovider.ToolCallDelta{
					Index: idx,
					ID:    tc.ID,
					Name:  tc.Function.Name,
				}
				toolArgs[idx] = &strings.Builder{}
				ch <- llmprovider.StreamEvent{
					Type: llmprovider.StreamEventToolCallStart,
					ToolCall: &llmprovider.ToolCallDelta{
						Index: idx,
						ID:    tc.ID,
						Name:  tc.Function.Name,
					},
				}
			}
			if tc.Function.Arguments != "" {
				toolArgs[idx].WriteString(tc.Function.Arguments)
				ch <- llmprovider.StreamEvent{
					Type: llmprovider.StreamEventToolCallDelta,
					ToolCall: &llmprovider.ToolCallDelta{
						Index:     idx,
						ArgsDelta: tc.Function.Arguments,
					},
				}
			}
		}

		if finishReason != nil && *finishReason != "" {
			// Finalize tool calls
			for idx, meta := range toolMeta {
				args := json.RawMessage(toolArgs[idx].String())
				ch <- llmprovider.StreamEvent{
					Type: llmprovider.StreamEventToolCallEnd,
					ToolCall: &llmprovider.ToolCallDelta{
						Index:     meta.Index,
						ID:        meta.ID,
						Name:      meta.Name,
						ArgsDelta: string(args),
					},
				}
				delete(toolMeta, idx)
				delete(toolArgs, idx)
			}

			var stopReason llmprovider.StopReason
			switch *finishReason {
			case "stop":
				stopReason = llmprovider.StopReasonStop
			case "length":
				stopReason = llmprovider.StopReasonLength
			case "tool_calls":
				stopReason = llmprovider.StopReasonToolUse
			default:
				stopReason = llmprovider.StopReason(*finishReason)
			}

			ch <- llmprovider.StreamEvent{
				Type:       llmprovider.StreamEventMessageEnd,
				StopReason: stopReason,
				Usage:      convertDashScopeUsage(chunk.Usage),
			}
		}
	}

	if err := scanner.Err(); err != nil && err != io.EOF {
		ch <- llmprovider.StreamEvent{Type: llmprovider.StreamEventError, Err: err}
	}
}

// ── DashScope SSE JSON structs ───────────────────────────────────────────────

type dashScopeChunk struct {
	Output dashScopeOutput `json:"output"`
	Usage  *dashScopeUsage `json:"usage"`
}

type dashScopeOutput struct {
	Choices []dashScopeChoice `json:"choices"`
}

type dashScopeChoice struct {
	Delta        dashScopeDelta `json:"delta"`
	FinishReason *string        `json:"finish_reason"`
}

type dashScopeDelta struct {
	Content   []dashScopeContentBlock `json:"content"`
	ToolCalls []dashScopeToolCall     `json:"tool_calls"`
}

type dashScopeContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type dashScopeToolCall struct {
	Index    int                    `json:"index"`
	ID       string                 `json:"id"`
	Type     string                 `json:"type"`
	Function dashScopeFunctionChunk `json:"function"`
}

type dashScopeFunctionChunk struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type dashScopeUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
}

func convertDashScopeUsage(u *dashScopeUsage) *llmprovider.Usage {
	if u == nil {
		return nil
	}
	return &llmprovider.Usage{
		InputTokens:  u.InputTokens,
		OutputTokens: u.OutputTokens,
	}
}
