// Package anthropic implements llmprovider.ApiProvider for the Anthropic Messages API.
package anthropic

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ubuildingagent/backend/llmprovider"
	"github.com/ubuildingagent/backend/llmprovider/providers/sseutil"
)

const (
	defaultBaseURL     = "https://api.anthropic.com"
	anthropicVersion   = "2023-06-01"
	defaultBetaHeaders = "interleaved-thinking-2025-05-14"
)

// Provider implements llmprovider.ApiProvider for the Anthropic Messages API.
type Provider struct {
	httpClient *http.Client
}

func New() *Provider {
	return &Provider{httpClient: &http.Client{Timeout: 10 * time.Minute}}
}

func NewWithClient(client *http.Client) *Provider {
	return &Provider{httpClient: client}
}

func (p *Provider) ApiType() llmprovider.ApiType {
	return llmprovider.ApiAnthropicMessages
}

func (p *Provider) Stream(
	ctx context.Context,
	model llmprovider.Model,
	conv llmprovider.Context,
	opts llmprovider.StreamOptions,
) <-chan llmprovider.StreamEvent {
	ch := make(chan llmprovider.StreamEvent, 32)
	go func() {
		defer close(ch)
		p.doStream(ctx, model, conv, opts, 0, 0, ch)
	}()
	return ch
}

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
		p.doStream(ctx, model, conv, opts.StreamOptions, maxTokens, budget, ch)
	}()
	return ch
}

func (p *Provider) doStream(
	ctx context.Context,
	model llmprovider.Model,
	conv llmprovider.Context,
	opts llmprovider.StreamOptions,
	maxTokens, thinkingBudget int,
	ch chan<- llmprovider.StreamEvent,
) {
	compat := resolveCompat(model)

	body := map[string]any{
		"model":  model.ID,
		"stream": true,
	}

	// Max tokens (required by Anthropic)
	mt := maxTokens
	if mt == 0 {
		mt = opts.MaxTokens
	}
	if mt == 0 {
		mt = model.MaxOutput
	}
	if mt == 0 {
		mt = 4096
	}
	body["max_tokens"] = mt

	// System prompt
	if conv.SystemPrompt != "" {
		body["system"] = conv.SystemPrompt
	}

	// Temperature
	if opts.Temperature != nil {
		body["temperature"] = *opts.Temperature
	}

	// Extended thinking
	if thinkingBudget > 0 {
		body["thinking"] = map[string]any{
			"type":         "enabled",
			"budget_tokens": thinkingBudget,
		}
	}

	// Messages
	body["messages"] = convertMessages(conv.Messages, compat)

	// Tools
	if len(conv.Tools) > 0 {
		body["tools"] = convertTools(conv.Tools, compat)
		body["tool_choice"] = map[string]any{"type": "auto"}
	}

	// Extra body
	for k, v := range opts.ExtraBody {
		body[k] = v
	}

	rawBody, err := json.Marshal(body)
	if err != nil {
		ch <- llmprovider.StreamEvent{Type: llmprovider.StreamEventError, Err: fmt.Errorf("marshal: %w", err)}
		return
	}

	if opts.OnPayload != nil {
		if modified := opts.OnPayload(rawBody, model); modified != nil {
			rawBody = modified
		}
	}

	baseURL := model.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	url := strings.TrimRight(baseURL, "/") + "/v1/messages"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(rawBody))
	if err != nil {
		ch <- llmprovider.StreamEvent{Type: llmprovider.StreamEventError, Err: fmt.Errorf("create request: %w", err)}
		return
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("anthropic-version", anthropicVersion)

	if thinkingBudget > 0 {
		req.Header.Set("anthropic-beta", defaultBetaHeaders)
	}

	apiKey := opts.APIKey
	if apiKey == "" {
		apiKey = model.Headers["x-api-key"]
	}
	if apiKey != "" {
		req.Header.Set("x-api-key", apiKey)
	}

	for k, v := range model.Headers {
		if k != "x-api-key" {
			req.Header.Set(k, v)
		}
	}
	for k, v := range opts.Headers {
		req.Header.Set(k, v)
	}

	timeoutClient := p.httpClient
	if opts.TimeoutMs > 0 {
		timeoutClient = &http.Client{Timeout: time.Duration(opts.TimeoutMs) * time.Millisecond}
	}

	resp, err := timeoutClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			ch <- llmprovider.StreamEvent{Type: llmprovider.StreamEventError, Err: ctx.Err()}
		} else {
			ch <- llmprovider.StreamEvent{Type: llmprovider.StreamEventError, Err: fmt.Errorf("http: %w", err)}
		}
		return
	}
	defer resp.Body.Close()

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

	parseAnthropicSSE(ctx, resp.Body, ch)
}

// ── Message conversion ──────────────────────────────────────────────────────

func resolveCompat(model llmprovider.Model) llmprovider.AnthropicMessagesCompat {
	c := model.AnthropicCompat()
	if c == nil {
		return llmprovider.AnthropicMessagesCompat{}
	}
	return *c
}

func convertMessages(messages []llmprovider.Message, _ llmprovider.AnthropicMessagesCompat) []map[string]any {
	out := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		switch msg.Role {
		case llmprovider.RoleUser:
			out = append(out, map[string]any{
				"role":    "user",
				"content": convertUserContent(msg.Content),
			})
		case llmprovider.RoleAssistant:
			out = append(out, map[string]any{
				"role":    "assistant",
				"content": convertAssistantContent(msg),
			})
		case llmprovider.RoleTool:
			out = append(out, map[string]any{
				"role":    "user",
				"content": convertToolResults(msg.Content),
			})
		}
	}
	return out
}

func convertUserContent(parts []llmprovider.ContentPart) []map[string]any {
	result := make([]map[string]any, 0, len(parts))
	for _, p := range parts {
		switch p.Type {
		case llmprovider.ContentTypeText:
			result = append(result, map[string]any{"type": "text", "text": p.Text})
		case llmprovider.ContentTypeImageURL:
			result = append(result, map[string]any{
				"type": "image",
				"source": map[string]any{
					"type": "url",
					"url":  p.ImageURL,
				},
			})
		case llmprovider.ContentTypeImageData:
			result = append(result, map[string]any{
				"type": "image",
				"source": map[string]any{
					"type":       "base64",
					"media_type": p.MimeType,
					"data":       base64.StdEncoding.EncodeToString(p.Data),
				},
			})
		case llmprovider.ContentTypeVideoURL:
			// Anthropic does not support video natively; include as text description
			result = append(result, map[string]any{
				"type": "text",
				"text": fmt.Sprintf("[Video URL: %s]", p.VideoURL),
			})
		}
	}
	return result
}

func convertAssistantContent(msg llmprovider.Message) []map[string]any {
	result := make([]map[string]any, 0)
	if msg.Thinking != "" {
		result = append(result, map[string]any{
			"type":     "thinking",
			"thinking": msg.Thinking,
		})
	}
	for _, p := range msg.Content {
		if p.Type == llmprovider.ContentTypeText && p.Text != "" {
			result = append(result, map[string]any{"type": "text", "text": p.Text})
		}
	}
	for _, tc := range msg.ToolCalls {
		result = append(result, map[string]any{
			"type":  "tool_use",
			"id":    tc.ID,
			"name":  tc.Name,
			"input": json.RawMessage(tc.Arguments),
		})
	}
	return result
}

func convertToolResults(parts []llmprovider.ContentPart) []map[string]any {
	result := make([]map[string]any, 0)
	for _, p := range parts {
		if p.Type == llmprovider.ContentTypeToolResult {
			result = append(result, map[string]any{
				"type":        "tool_result",
				"tool_use_id": p.ToolCallID,
				"content":     p.ToolResult,
			})
		}
	}
	return result
}

func convertTools(tools []llmprovider.Tool, compat llmprovider.AnthropicMessagesCompat) []map[string]any {
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		var params any
		if len(t.Parameters) > 0 {
			_ = json.Unmarshal(t.Parameters, &params)
		}
		tool := map[string]any{
			"name":         t.Name,
			"description":  t.Description,
			"input_schema": params,
		}
		out = append(out, tool)
	}
	_ = compat // future: cache_control on tools
	return out
}

// ── SSE parsing ─────────────────────────────────────────────────────────────

func parseAnthropicSSE(ctx context.Context, r io.Reader, ch chan<- llmprovider.StreamEvent) {
	scanner := sseutil.NewScanner(r)
	var inputTokens, outputTokens int
	var currentThinkingIdx = -1

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

		var raw map[string]json.RawMessage
		if err := json.Unmarshal([]byte(ev.Data), &raw); err != nil {
			continue
		}

		var evType string
		if t, ok := raw["type"]; ok {
			_ = json.Unmarshal(t, &evType)
		}

		switch evType {
		case "message_start":
			var ms anthropicMessageStart
			if err := json.Unmarshal([]byte(ev.Data), &ms); err == nil {
				inputTokens = ms.Message.Usage.InputTokens
			}

		case "content_block_start":
			var cbs anthropicContentBlockStart
			if err := json.Unmarshal([]byte(ev.Data), &cbs); err == nil {
				switch cbs.ContentBlock.Type {
				case "thinking":
					currentThinkingIdx = cbs.Index
				case "tool_use":
					ch <- llmprovider.StreamEvent{
						Type: llmprovider.StreamEventToolCallStart,
						ToolCall: &llmprovider.ToolCallDelta{
							Index: cbs.Index,
							ID:    cbs.ContentBlock.ID,
							Name:  cbs.ContentBlock.Name,
						},
					}
				}
			}

		case "content_block_delta":
			var cbd anthropicContentBlockDelta
			if err := json.Unmarshal([]byte(ev.Data), &cbd); err == nil {
				switch cbd.Delta.Type {
				case "text_delta":
					ch <- llmprovider.StreamEvent{
						Type:  llmprovider.StreamEventTextDelta,
						Delta: cbd.Delta.Text,
					}
				case "thinking_delta":
					ch <- llmprovider.StreamEvent{
						Type:  llmprovider.StreamEventThinkingDelta,
						Delta: cbd.Delta.Thinking,
					}
				case "input_json_delta":
					ch <- llmprovider.StreamEvent{
						Type: llmprovider.StreamEventToolCallDelta,
						ToolCall: &llmprovider.ToolCallDelta{
							Index:     cbd.Index,
							ArgsDelta: cbd.Delta.PartialJSON,
						},
					}
				}
			}

		case "content_block_stop":
			var cbs anthropicContentBlockStop
			if err := json.Unmarshal([]byte(ev.Data), &cbs); err == nil {
				if cbs.Index == currentThinkingIdx {
					currentThinkingIdx = -1
				}
			}

		case "message_delta":
			var md anthropicMessageDelta
			if err := json.Unmarshal([]byte(ev.Data), &md); err == nil {
				outputTokens = md.Usage.OutputTokens

				var stopReason llmprovider.StopReason
				switch md.Delta.StopReason {
				case "end_turn":
					stopReason = llmprovider.StopReasonStop
				case "max_tokens":
					stopReason = llmprovider.StopReasonLength
				case "tool_use":
					stopReason = llmprovider.StopReasonToolUse
				default:
					stopReason = llmprovider.StopReason(md.Delta.StopReason)
				}

				ch <- llmprovider.StreamEvent{
					Type:       llmprovider.StreamEventMessageEnd,
					StopReason: stopReason,
					Usage: &llmprovider.Usage{
						InputTokens:  inputTokens,
						OutputTokens: outputTokens,
					},
				}
			}
		}
	}

	if err := scanner.Err(); err != nil && err != io.EOF {
		ch <- llmprovider.StreamEvent{Type: llmprovider.StreamEventError, Err: err}
	}
}

type anthropicMessageStart struct {
	Message struct {
		Usage struct {
			InputTokens int `json:"input_tokens"`
		} `json:"usage"`
	} `json:"message"`
}

type anthropicContentBlockStart struct {
	Index        int `json:"index"`
	ContentBlock struct {
		Type string `json:"type"`
		ID   string `json:"id"`   // tool_use
		Name string `json:"name"` // tool_use
	} `json:"content_block"`
}

type anthropicContentBlockDelta struct {
	Index int `json:"index"`
	Delta struct {
		Type        string `json:"type"`
		Text        string `json:"text"`         // text_delta
		Thinking    string `json:"thinking"`     // thinking_delta
		PartialJSON string `json:"partial_json"` // input_json_delta
	} `json:"delta"`
}

type anthropicContentBlockStop struct {
	Index int `json:"index"`
}

type anthropicMessageDelta struct {
	Delta struct {
		StopReason string `json:"stop_reason"`
	} `json:"delta"`
	Usage struct {
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}
