package openaicompat

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ubuildingagent/backend/llmprovider"
	"github.com/ubuildingagent/backend/llmprovider/providers/sseutil"
)

// Provider implements llmprovider.ApiProvider for OpenAI-compatible APIs.
// A single instance handles any provider that speaks OpenAI chat completions
// (OpenAI, DeepSeek, Groq, Qwen-compat, Volcengine Ark, MiniMax, etc.).
type Provider struct {
	httpClient *http.Client
}

// New creates a Provider with a default HTTP client.
func New() *Provider {
	return &Provider{
		httpClient: &http.Client{Timeout: 10 * time.Minute},
	}
}

// NewWithClient creates a Provider with a custom HTTP client (useful for testing).
func NewWithClient(client *http.Client) *Provider {
	return &Provider{httpClient: client}
}

func (p *Provider) ApiType() llmprovider.ApiType {
	return llmprovider.ApiOpenAICompletions
}

// Stream implements ApiProvider using provider-specific StreamOptions.
func (p *Provider) Stream(
	ctx context.Context,
	model llmprovider.Model,
	conv llmprovider.Context,
	opts llmprovider.StreamOptions,
) <-chan llmprovider.StreamEvent {
	ch := make(chan llmprovider.StreamEvent, 32)
	go func() {
		defer close(ch)
		p.stream(ctx, model, conv, opts, 0, nil, ch)
	}()
	return ch
}

// StreamSimple implements ApiProvider with ThinkingLevel → budget translation.
func (p *Provider) StreamSimple(
	ctx context.Context,
	model llmprovider.Model,
	conv llmprovider.Context,
	opts llmprovider.SimpleStreamOptions,
) <-chan llmprovider.StreamEvent {
	ch := make(chan llmprovider.StreamEvent, 32)
	go func() {
		defer close(ch)
		maxTokens, budget := llmprovider.AdjustMaxTokensForThinking(
			opts.MaxTokens, model.MaxOutput, opts.Reasoning, opts.ThinkingBudgets,
		)
		p.stream(ctx, model, conv, opts.StreamOptions, maxTokens, &thinkingParams{
			level:  opts.Reasoning,
			budget: budget,
		}, ch)
	}()
	return ch
}

type thinkingParams struct {
	level  llmprovider.ThinkingLevel
	budget int
}

func (p *Provider) stream(
	ctx context.Context,
	model llmprovider.Model,
	conv llmprovider.Context,
	opts llmprovider.StreamOptions,
	maxTokens int,
	thinking *thinkingParams,
	ch chan<- llmprovider.StreamEvent,
) {
	compat := ResolveCompat(model)

	// ── Build request body ──
	body := map[string]any{
		"model":  model.ID,
		"stream": true,
	}

	// Messages
	body["messages"] = ConvertMessages(conv.Messages, conv.SystemPrompt, compat)

	// Tools
	if tools := ConvertTools(conv.Tools, compat); tools != nil {
		body["tools"] = tools
		body["tool_choice"] = "auto"
	}

	// Max tokens
	mtField := "max_tokens"
	if compat.MaxTokensField != "" {
		mtField = compat.MaxTokensField
	}
	if maxTokens > 0 {
		body[mtField] = maxTokens
	} else if opts.MaxTokens > 0 {
		body[mtField] = opts.MaxTokens
	}

	// Temperature
	if opts.Temperature != nil {
		body["temperature"] = *opts.Temperature
	}

	// Stream options (usage)
	if llmprovider.BoolVal(compat.SupportsUsageInStreaming, true) {
		body["stream_options"] = map[string]any{"include_usage": true}
	}

	// Thinking / reasoning
	if thinking != nil && thinking.level != llmprovider.ThinkingLevelOff {
		BuildReasoningParams(body, thinking.level, thinking.budget, compat)
	}

	// Extra body fields
	for k, v := range opts.ExtraBody {
		body[k] = v
	}

	// OnPayload hook
	rawBody, err := json.Marshal(body)
	if err != nil {
		ch <- llmprovider.StreamEvent{Type: llmprovider.StreamEventError, Err: fmt.Errorf("marshal request: %w", err)}
		return
	}
	if opts.OnPayload != nil {
		if modified := opts.OnPayload(rawBody, model); modified != nil {
			rawBody = modified
		}
	}

	// ── Build HTTP request ──
	baseURL := strings.TrimRight(model.BaseURL, "/")
	url := baseURL + "/chat/completions"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(rawBody))
	if err != nil {
		ch <- llmprovider.StreamEvent{Type: llmprovider.StreamEventError, Err: fmt.Errorf("create request: %w", err)}
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	// API key
	apiKey := opts.APIKey
	if apiKey == "" {
		apiKey = model.Headers["Authorization"]
	}
	if apiKey != "" && !strings.HasPrefix(apiKey, "Bearer ") {
		req.Header.Set("Authorization", "Bearer "+apiKey)
	} else if apiKey != "" {
		req.Header.Set("Authorization", apiKey)
	}

	// Per-model headers
	for k, v := range model.Headers {
		if k != "Authorization" {
			req.Header.Set(k, v)
		}
	}
	// Per-request headers
	for k, v := range opts.Headers {
		req.Header.Set(k, v)
	}

	// ── Send request ──
	timeoutClient := p.httpClient
	if opts.TimeoutMs > 0 {
		timeoutClient = &http.Client{Timeout: time.Duration(opts.TimeoutMs) * time.Millisecond}
	}

	resp, err := timeoutClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			ch <- llmprovider.StreamEvent{Type: llmprovider.StreamEventError, Err: fmt.Errorf("request cancelled: %w", ctx.Err())}
		} else {
			ch <- llmprovider.StreamEvent{Type: llmprovider.StreamEventError, Err: fmt.Errorf("http request: %w", err)}
		}
		return
	}
	defer resp.Body.Close()

	// OnResponse hook
	if opts.OnResponse != nil {
		headers := make(http.Header)
		for k, v := range resp.Header {
			headers[k] = v
		}
		opts.OnResponse(resp.StatusCode, headers, model)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		ch <- llmprovider.StreamEvent{
			Type: llmprovider.StreamEventError,
			Err:  fmt.Errorf("http %d: %s", resp.StatusCode, strings.TrimSpace(string(body))),
		}
		return
	}

	// ── Parse SSE ──
	parseSSE(ctx, resp.Body, ch)
}

// parseSSE reads OpenAI-style SSE chunks and emits StreamEvents.
func parseSSE(ctx context.Context, r io.Reader, ch chan<- llmprovider.StreamEvent) {
	scanner := sseutil.NewScanner(r)
	// Track in-progress tool calls by index
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
		if ev.IsDone() {
			break
		}
		if ev.Data == "" {
			continue
		}

		var chunk openAIChunk
		if err := json.Unmarshal([]byte(ev.Data), &chunk); err != nil {
			continue // skip malformed chunks
		}

		if len(chunk.Choices) == 0 {
			// Usage-only chunk
			if chunk.Usage != nil {
				ch <- llmprovider.StreamEvent{
					Type:  llmprovider.StreamEventMessageEnd,
					Usage: convertUsage(chunk.Usage),
				}
			}
			continue
		}

		delta := chunk.Choices[0].Delta
		finishReason := chunk.Choices[0].FinishReason

		// Thinking / reasoning content (DeepSeek reasoning_content field)
		if delta.ReasoningContent != "" {
			ch <- llmprovider.StreamEvent{
				Type:  llmprovider.StreamEventThinkingDelta,
				Delta: delta.ReasoningContent,
			}
		}

		// Text content
		if delta.Content != "" {
			ch <- llmprovider.StreamEvent{
				Type:  llmprovider.StreamEventTextDelta,
				Delta: delta.Content,
			}
		}

		// Tool calls
		for _, tc := range delta.ToolCalls {
			idx := tc.Index
			if _, ok := toolMeta[idx]; !ok {
				// First chunk for this tool call
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

		// Finish reason
		if finishReason != "" {
			// Finalize any open tool calls
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
			switch finishReason {
			case "stop":
				stopReason = llmprovider.StopReasonStop
			case "length":
				stopReason = llmprovider.StopReasonLength
			case "tool_calls":
				stopReason = llmprovider.StopReasonToolUse
			default:
				stopReason = llmprovider.StopReason(finishReason)
			}

			usage := convertUsage(chunk.Usage)
			ch <- llmprovider.StreamEvent{
				Type:       llmprovider.StreamEventMessageEnd,
				StopReason: stopReason,
				Usage:      usage,
			}
		}
	}

	if err := scanner.Err(); err != nil && err != io.EOF {
		ch <- llmprovider.StreamEvent{Type: llmprovider.StreamEventError, Err: err}
	}
}

// ── JSON structs for OpenAI streaming response ──────────────────────────────

type openAIChunk struct {
	ID      string          `json:"id"`
	Choices []chunkChoice   `json:"choices"`
	Usage   *openAIUsage    `json:"usage"`
	Model   string          `json:"model"`
}

type chunkChoice struct {
	Index        int        `json:"index"`
	Delta        chunkDelta `json:"delta"`
	FinishReason string     `json:"finish_reason"`
}

type chunkDelta struct {
	Role             string          `json:"role"`
	Content          string          `json:"content"`
	ReasoningContent string          `json:"reasoning_content"` // DeepSeek
	ToolCalls        []toolCallChunk `json:"tool_calls"`
}

type toolCallChunk struct {
	Index    int              `json:"index"`
	ID       string           `json:"id"`
	Type     string           `json:"type"`
	Function toolFunctionChunk `json:"function"`
}

type toolFunctionChunk struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openAIUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

func convertUsage(u *openAIUsage) *llmprovider.Usage {
	if u == nil {
		return nil
	}
	return &llmprovider.Usage{
		InputTokens:  u.PromptTokens,
		OutputTokens: u.CompletionTokens,
	}
}
