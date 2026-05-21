package qwen

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
)

const defaultBaseURL = "https://dashscope.aliyuncs.com/api/v1"

// Provider implements llmprovider.ApiProvider for the DashScope native API.
// For the OpenAI-compatible DashScope endpoint, use openaicompat.Provider instead.
type Provider struct {
	httpClient *http.Client
}

// New creates a Provider with a default HTTP client.
func New() *Provider {
	return &Provider{httpClient: &http.Client{Timeout: 10 * time.Minute}}
}

// NewWithClient creates a Provider with a custom HTTP client (for testing).
func NewWithClient(client *http.Client) *Provider {
	return &Provider{httpClient: client}
}

func (p *Provider) ApiType() llmprovider.ApiType {
	return llmprovider.ApiDashScopeMessages
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
	// Build request body (DashScope input format)
	reqBody := map[string]any{
		"model": model.ID,
		"input": map[string]any{
			"messages": convertMessages(conv.Messages, conv.SystemPrompt),
		},
		"parameters": map[string]any{},
	}

	params := reqBody["parameters"].(map[string]any)
	if maxTokens > 0 {
		params["max_tokens"] = maxTokens
	} else if opts.MaxTokens > 0 {
		params["max_tokens"] = opts.MaxTokens
	}
	if opts.Temperature != nil {
		params["temperature"] = *opts.Temperature
	}

	// Tools
	if tools := convertTools(conv.Tools); tools != nil {
		reqBody["tools"] = tools
	}

	// Extra body
	for k, v := range opts.ExtraBody {
		reqBody[k] = v
	}

	rawBody, err := json.Marshal(reqBody)
	if err != nil {
		ch <- llmprovider.StreamEvent{Type: llmprovider.StreamEventError, Err: fmt.Errorf("marshal: %w", err)}
		return
	}

	// OnPayload hook
	if opts.OnPayload != nil {
		if modified := opts.OnPayload(rawBody, model); modified != nil {
			rawBody = modified
		}
	}

	// Build URL
	baseURL := model.BaseURL
	if baseURL == "" {
		baseURL = defaultBaseURL
	}
	url := strings.TrimRight(baseURL, "/") + "/services/aigc/multimodal-generation/generation"

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(rawBody))
	if err != nil {
		ch <- llmprovider.StreamEvent{Type: llmprovider.StreamEventError, Err: fmt.Errorf("create request: %w", err)}
		return
	}

	req.Header.Set("Content-Type", "application/json")
	// DashScope SSE enable header
	req.Header.Set("X-DashScope-SSE", "enable")
	req.Header.Set("Accept", "text/event-stream")

	// Auth
	apiKey := opts.APIKey
	if apiKey == "" {
		apiKey = model.Headers["Authorization"]
	}
	if apiKey != "" {
		if !strings.HasPrefix(apiKey, "Bearer ") {
			req.Header.Set("Authorization", "Bearer "+apiKey)
		} else {
			req.Header.Set("Authorization", apiKey)
		}
	}

	for k, v := range model.Headers {
		if k != "Authorization" {
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

	ParseDashScopeSSE(ctx, resp.Body, ch)
}
