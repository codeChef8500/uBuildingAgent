// Package google implements llmprovider.ApiProvider for the Google Generative AI API.
package google

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

const defaultBaseURL = "https://generativelanguage.googleapis.com"

// Provider implements llmprovider.ApiProvider for Google Generative AI (Gemini).
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
	return llmprovider.ApiGoogleGenerativeAI
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
		p.doStream(ctx, model, conv, opts, 0, ch)
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
		maxTokens, _ := llmprovider.AdjustMaxTokensForThinking(
			opts.MaxTokens, model.MaxOutput, opts.Reasoning, opts.ThinkingBudgets,
		)
		p.doStream(ctx, model, conv, opts.StreamOptions, maxTokens, ch)
	}()
	return ch
}

func (p *Provider) doStream(
	ctx context.Context,
	model llmprovider.Model,
	conv llmprovider.Context,
	opts llmprovider.StreamOptions,
	maxTokens int,
	ch chan<- llmprovider.StreamEvent,
) {
	body := map[string]any{}

	// System instruction
	if conv.SystemPrompt != "" {
		body["systemInstruction"] = map[string]any{
			"parts": []map[string]any{{"text": conv.SystemPrompt}},
		}
	}

	// Contents (messages)
	body["contents"] = convertMessages(conv.Messages)

	// Tools
	if len(conv.Tools) > 0 {
		body["tools"] = convertTools(conv.Tools)
		body["toolConfig"] = map[string]any{
			"functionCallingConfig": map[string]any{"mode": "AUTO"},
		}
	}

	// Generation config
	genCfg := map[string]any{}
	mt := maxTokens
	if mt == 0 {
		mt = opts.MaxTokens
	}
	if mt > 0 {
		genCfg["maxOutputTokens"] = mt
	}
	if opts.Temperature != nil {
		genCfg["temperature"] = *opts.Temperature
	}
	if len(genCfg) > 0 {
		body["generationConfig"] = genCfg
	}

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
	url := fmt.Sprintf("%s/v1beta/models/%s:streamGenerateContent?alt=sse",
		strings.TrimRight(baseURL, "/"), model.ID)

	// API key via query param (Google style) or header
	apiKey := opts.APIKey
	if apiKey == "" {
		apiKey = model.Headers["x-goog-api-key"]
	}
	if apiKey != "" && !strings.HasPrefix(apiKey, "Bearer ") {
		url += "&key=" + apiKey
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(rawBody))
	if err != nil {
		ch <- llmprovider.StreamEvent{Type: llmprovider.StreamEventError, Err: fmt.Errorf("create request: %w", err)}
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")

	// Bearer token auth (Vertex AI / service account)
	if strings.HasPrefix(apiKey, "Bearer ") {
		req.Header.Set("Authorization", apiKey)
	}
	for k, v := range model.Headers {
		req.Header.Set(k, v)
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

	parseGoogleSSE(ctx, resp.Body, ch)
}

// ── Message conversion ──────────────────────────────────────────────────────

func convertMessages(messages []llmprovider.Message) []map[string]any {
	out := make([]map[string]any, 0, len(messages))
	for _, msg := range messages {
		switch msg.Role {
		case llmprovider.RoleUser:
			out = append(out, map[string]any{
				"role":  "user",
				"parts": convertUserParts(msg.Content),
			})
		case llmprovider.RoleAssistant:
			out = append(out, map[string]any{
				"role":  "model",
				"parts": convertAssistantParts(msg),
			})
		case llmprovider.RoleTool:
			out = append(out, map[string]any{
				"role":  "user",
				"parts": convertToolResultParts(msg.Content),
			})
		}
	}
	return out
}

func convertUserParts(parts []llmprovider.ContentPart) []map[string]any {
	result := make([]map[string]any, 0, len(parts))
	for _, p := range parts {
		switch p.Type {
		case llmprovider.ContentTypeText:
			result = append(result, map[string]any{"text": p.Text})
		case llmprovider.ContentTypeImageURL:
			// Google supports fileData for URLs or inline_data for base64
			result = append(result, map[string]any{
				"fileData": map[string]any{
					"mimeType": "image/jpeg",
					"fileUri":  p.ImageURL,
				},
			})
		case llmprovider.ContentTypeImageData:
			result = append(result, map[string]any{
				"inlineData": map[string]any{
					"mimeType": p.MimeType,
					"data":     base64.StdEncoding.EncodeToString(p.Data),
				},
			})
		case llmprovider.ContentTypeVideoURL:
			// Gemini supports video via fileData URI
			result = append(result, map[string]any{
				"fileData": map[string]any{
					"mimeType": "video/mp4",
					"fileUri":  p.VideoURL,
				},
			})
		case llmprovider.ContentTypeVideoFrame:
			if !llmprovider.IsVideoFrameGroup(p) {
				// Single video frame as inline image
				result = append(result, map[string]any{
					"inlineData": map[string]any{
						"mimeType": p.MimeType,
						"data":     base64.StdEncoding.EncodeToString(p.Data),
					},
				})
			}
			// Group sentinels handled by caller (not needed here — Google prefers fileData)
		}
	}
	return result
}

func convertAssistantParts(msg llmprovider.Message) []map[string]any {
	result := make([]map[string]any, 0)
	// Thinking as thought (Google Gemini 2.5+)
	if msg.Thinking != "" {
		result = append(result, map[string]any{
			"thought": true,
			"text":    msg.Thinking,
		})
	}
	for _, p := range msg.Content {
		if p.Type == llmprovider.ContentTypeText && p.Text != "" {
			result = append(result, map[string]any{"text": p.Text})
		}
	}
	for _, tc := range msg.ToolCalls {
		var args map[string]any
		_ = json.Unmarshal(tc.Arguments, &args)
		fc := map[string]any{
			"functionCall": map[string]any{
				"name": tc.Name,
				"args": args,
			},
		}
		if tc.ThoughtSignature != "" {
			fc["thoughtSignature"] = tc.ThoughtSignature
		}
		result = append(result, fc)
	}
	return result
}

func convertToolResultParts(parts []llmprovider.ContentPart) []map[string]any {
	result := make([]map[string]any, 0)
	for _, p := range parts {
		if p.Type == llmprovider.ContentTypeToolResult {
			result = append(result, map[string]any{
				"functionResponse": map[string]any{
					"name":     p.ToolCallID,
					"response": map[string]any{"output": p.ToolResult},
				},
			})
		}
	}
	return result
}

func convertTools(tools []llmprovider.Tool) []map[string]any {
	declarations := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		var params any
		if len(t.Parameters) > 0 {
			_ = json.Unmarshal(t.Parameters, &params)
		}
		declarations = append(declarations, map[string]any{
			"name":        t.Name,
			"description": t.Description,
			"parameters":  params,
		})
	}
	return []map[string]any{{"functionDeclarations": declarations}}
}

// ── SSE parsing ─────────────────────────────────────────────────────────────

func parseGoogleSSE(ctx context.Context, r io.Reader, ch chan<- llmprovider.StreamEvent) {
	scanner := sseutil.NewScanner(r)

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

		var chunk geminiChunk
		if err := json.Unmarshal([]byte(ev.Data), &chunk); err != nil {
			continue
		}

		if len(chunk.Candidates) == 0 {
			continue
		}

		cand := chunk.Candidates[0]
		for _, part := range cand.Content.Parts {
			if part.Text != "" {
				if part.Thought {
					ch <- llmprovider.StreamEvent{
						Type:  llmprovider.StreamEventThinkingDelta,
						Delta: part.Text,
					}
				} else {
					ch <- llmprovider.StreamEvent{
						Type:  llmprovider.StreamEventTextDelta,
						Delta: part.Text,
					}
				}
			}
			if part.FunctionCall != nil {
				argsJSON, _ := json.Marshal(part.FunctionCall.Args)
				ch <- llmprovider.StreamEvent{
					Type: llmprovider.StreamEventToolCallStart,
					ToolCall: &llmprovider.ToolCallDelta{
						Index: 0,
						Name:  part.FunctionCall.Name,
					},
				}
				ch <- llmprovider.StreamEvent{
					Type: llmprovider.StreamEventToolCallEnd,
					ToolCall: &llmprovider.ToolCallDelta{
						Index:     0,
						Name:      part.FunctionCall.Name,
						ArgsDelta: string(argsJSON),
					},
				}
			}
		}

		if cand.FinishReason != "" {
			var stopReason llmprovider.StopReason
			switch cand.FinishReason {
			case "STOP":
				stopReason = llmprovider.StopReasonStop
			case "MAX_TOKENS":
				stopReason = llmprovider.StopReasonLength
			default:
				stopReason = llmprovider.StopReason(strings.ToLower(cand.FinishReason))
			}
			var usage *llmprovider.Usage
			if m := chunk.UsageMetadata; m != nil {
				usage = &llmprovider.Usage{
					InputTokens:  m.PromptTokenCount,
					OutputTokens: m.CandidatesTokenCount,
				}
			}
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

// ── Gemini JSON structs ──────────────────────────────────────────────────────

type geminiChunk struct {
	Candidates    []geminiCandidate `json:"candidates"`
	UsageMetadata *geminiUsage      `json:"usageMetadata"`
}

type geminiCandidate struct {
	Content      geminiContent `json:"content"`
	FinishReason string        `json:"finishReason"`
	Index        int           `json:"index"`
}

type geminiContent struct {
	Parts []geminiPart `json:"parts"`
	Role  string       `json:"role"`
}

type geminiPart struct {
	Text         string            `json:"text"`
	Thought      bool              `json:"thought"`
	FunctionCall *geminiFuncCall   `json:"functionCall"`
}

type geminiFuncCall struct {
	Name string         `json:"name"`
	Args map[string]any `json:"args"`
}

type geminiUsage struct {
	PromptTokenCount     int `json:"promptTokenCount"`
	CandidatesTokenCount int `json:"candidatesTokenCount"`
	TotalTokenCount      int `json:"totalTokenCount"`
}
