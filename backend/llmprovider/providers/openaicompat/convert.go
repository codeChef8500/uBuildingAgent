package openaicompat

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/ubuildingagent/backend/llmprovider"
)

// ConvertMessages converts internal Messages to OpenAI chat format.
// compat controls format quirks (developer role, tool result name, etc.)
func ConvertMessages(
	messages []llmprovider.Message,
	systemPrompt string,
	compat llmprovider.OpenAICompletionsCompat,
) []map[string]any {
	out := make([]map[string]any, 0, len(messages)+1)

	if systemPrompt != "" {
		role := "system"
		if llmprovider.BoolVal(compat.SupportsDeveloperRole, false) {
			role = "developer"
		}
		out = append(out, map[string]any{"role": role, "content": systemPrompt})
	}

	for i, msg := range messages {
		switch msg.Role {
		case llmprovider.RoleSystem:
			// Mid-conversation system messages (e.g. from ContextModifier injections).
			text := ""
			for _, p := range msg.Content {
				if p.Type == llmprovider.ContentTypeText {
					text += p.Text
				}
			}
			role := "system"
			if llmprovider.BoolVal(compat.SupportsDeveloperRole, false) {
				role = "developer"
			}
			out = append(out, map[string]any{"role": role, "content": text})
		case llmprovider.RoleUser:
			out = append(out, convertUserMessage(msg, compat))
		case llmprovider.RoleAssistant:
			out = append(out, convertAssistantMessage(msg, compat))
		case llmprovider.RoleTool:
			out = append(out, convertToolResultMessage(msg, compat))
			// Some providers require an empty assistant message after tool results
			if llmprovider.BoolVal(compat.RequiresAssistantAfterToolResult, false) {
				if i == len(messages)-1 || messages[i+1].Role != llmprovider.RoleAssistant {
					out = append(out, map[string]any{"role": "assistant", "content": ""})
				}
			}
		}
	}
	return out
}

func convertUserMessage(msg llmprovider.Message, compat llmprovider.OpenAICompletionsCompat) map[string]any {
	content := convertContentParts(msg.Content, compat)
	// If all parts are plain text, collapse to a string for cleaner payloads
	if len(content) == 1 {
		if t, ok := content[0].(map[string]any); ok {
			if t["type"] == "text" {
				return map[string]any{"role": "user", "content": t["text"]}
			}
		}
	}
	return map[string]any{"role": "user", "content": content}
}

func convertAssistantMessage(msg llmprovider.Message, compat llmprovider.OpenAICompletionsCompat) map[string]any {
	m := map[string]any{"role": "assistant"}

	// Text content
	var textContent string
	var thinkingText string
	for _, p := range msg.Content {
		if p.Type == llmprovider.ContentTypeText {
			textContent += p.Text
		}
	}
	if msg.Thinking != "" {
		thinkingText = msg.Thinking
	}

	// Providers that require thinking as <thinking>...</thinking> text block
	if thinkingText != "" && llmprovider.BoolVal(compat.RequiresThinkingAsText, false) {
		textContent = "<thinking>" + thinkingText + "</thinking>\n" + textContent
	}

	if len(msg.ToolCalls) > 0 {
		m["content"] = nil
		tcs := make([]map[string]any, 0, len(msg.ToolCalls))
		for _, tc := range msg.ToolCalls {
			tcs = append(tcs, map[string]any{
				"id":   tc.ID,
				"type": "function",
				"function": map[string]any{
					"name":      tc.Name,
					"arguments": string(tc.Arguments),
				},
			})
		}
		m["tool_calls"] = tcs
	} else {
		m["content"] = textContent
	}

	// DeepSeek / reasoning_content replay
	if llmprovider.BoolVal(compat.RequiresReasoningContentOnReplayed, false) {
		if thinkingText != "" {
			m["reasoning_content"] = thinkingText
		} else {
			m["reasoning_content"] = ""
		}
	}
	return m
}

func convertToolResultMessage(msg llmprovider.Message, compat llmprovider.OpenAICompletionsCompat) map[string]any {
	// Extract tool call ID and result text from content parts
	var toolCallID, resultText string
	for _, p := range msg.Content {
		if p.Type == llmprovider.ContentTypeToolResult {
			toolCallID = p.ToolCallID
			resultText = p.ToolResult
		} else if p.Type == llmprovider.ContentTypeText {
			resultText += p.Text
		}
	}
	m := map[string]any{
		"role":         "tool",
		"tool_call_id": toolCallID,
		"content":      resultText,
	}
	if llmprovider.BoolVal(compat.RequiresToolResultName, false) {
		m["name"] = toolCallID // fallback: use ID as name
	}
	return m
}

func convertContentParts(parts []llmprovider.ContentPart, compat llmprovider.OpenAICompletionsCompat) []any {
	result := make([]any, 0, len(parts))
	i := 0
	for i < len(parts) {
		p := parts[i]
		switch p.Type {
		case llmprovider.ContentTypeText:
			result = append(result, map[string]any{"type": "text", "text": p.Text})
		case llmprovider.ContentTypeImageURL:
			result = append(result, map[string]any{
				"type":      "image_url",
				"image_url": map[string]any{"url": p.ImageURL},
			})
		case llmprovider.ContentTypeImageData:
			dataURL := fmt.Sprintf("data:%s;base64,%s",
				p.MimeType,
				base64.StdEncoding.EncodeToString(p.Data),
			)
			result = append(result, map[string]any{
				"type":      "image_url",
				"image_url": map[string]any{"url": dataURL},
			})
		case llmprovider.ContentTypeVideoURL:
			if compat.QwenVideoMode {
				v := map[string]any{"type": "video", "video": p.VideoURL}
				if p.VideoFPS > 0 {
					v["fps"] = p.VideoFPS
				}
				if p.VideoNFrames > 0 {
					v["nframes"] = p.VideoNFrames
				}
				result = append(result, v)
			} else {
				// Non-Qwen: fall back to text description
				result = append(result, map[string]any{
					"type": "text",
					"text": fmt.Sprintf("[Video: %s]", p.VideoURL),
				})
			}
		case llmprovider.ContentTypeVideoFrame:
			if compat.QwenVideoMode {
				// Collect all consecutive frames into an array
				if llmprovider.IsVideoFrameGroup(p) {
					// Sentinel: next VideoNFrames parts are the actual frames
					nf := p.VideoNFrames
					frames := make([]string, 0, nf)
					for j := 0; j < nf && i+1+j < len(parts); j++ {
						f := parts[i+1+j]
						frames = append(frames, base64.StdEncoding.EncodeToString(f.Data))
					}
					result = append(result, map[string]any{
						"type":  "video",
						"video": frames,
					})
					i += nf // skip the frame parts
				} else {
					// Single frame — treat as image
					dataURL := fmt.Sprintf("data:%s;base64,%s",
						p.MimeType,
						base64.StdEncoding.EncodeToString(p.Data),
					)
					result = append(result, map[string]any{
						"type":      "image_url",
						"image_url": map[string]any{"url": dataURL},
					})
				}
			} else {
				dataURL := fmt.Sprintf("data:%s;base64,%s",
					p.MimeType,
					base64.StdEncoding.EncodeToString(p.Data),
				)
				result = append(result, map[string]any{
					"type":      "image_url",
					"image_url": map[string]any{"url": dataURL},
				})
			}
		}
		i++
	}
	return result
}

// ConvertTools converts internal Tool definitions to OpenAI tools format.
func ConvertTools(tools []llmprovider.Tool, compat llmprovider.OpenAICompletionsCompat) []map[string]any {
	if len(tools) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		var params any
		if len(t.Parameters) > 0 {
			_ = json.Unmarshal(t.Parameters, &params)
		}
		fn := map[string]any{
			"name":        t.Name,
			"description": t.Description,
			"parameters":  params,
		}
		if llmprovider.BoolVal(compat.SupportsStrictMode, true) {
			fn["strict"] = true
		}
		out = append(out, map[string]any{"type": "function", "function": fn})
	}
	return out
}

// BuildReasoningParams adds thinking/reasoning parameters to the request body.
func BuildReasoningParams(
	body map[string]any,
	level llmprovider.ThinkingLevel,
	thinkingBudget int,
	compat llmprovider.OpenAICompletionsCompat,
) {
	if level == llmprovider.ThinkingLevelOff || level == "" {
		return
	}
	effortStr := thinkingLevelToEffort(level)

	switch compat.ThinkingFormat {
	case llmprovider.ThinkingFormatDeepSeek:
		body["thinking"] = map[string]any{
			"type":          "enabled_budget",
			"budget_tokens": thinkingBudget,
		}
		body["reasoning_effort"] = effortStr
	case llmprovider.ThinkingFormatQwen:
		body["enable_thinking"] = true
	case llmprovider.ThinkingFormatQwenChatTpl:
		body["chat_template_kwargs"] = map[string]any{"enable_thinking": true}
	case llmprovider.ThinkingFormatTogether:
		body["reasoning"] = map[string]any{"enabled": true}
		if llmprovider.BoolVal(compat.SupportsReasoningEffort, false) {
			body["reasoning_effort"] = effortStr
		}
	case llmprovider.ThinkingFormatOpenRouter:
		body["reasoning"] = map[string]any{"effort": effortStr}
	default: // "openai" or empty
		if llmprovider.BoolVal(compat.SupportsReasoningEffort, false) {
			body["reasoning_effort"] = effortStr
		}
	}
}

func thinkingLevelToEffort(level llmprovider.ThinkingLevel) string {
	switch level {
	case llmprovider.ThinkingLevelMinimal, llmprovider.ThinkingLevelLow:
		return "low"
	case llmprovider.ThinkingLevelMedium:
		return "medium"
	case llmprovider.ThinkingLevelHigh, llmprovider.ThinkingLevelXHigh:
		return "high"
	default:
		return "medium"
	}
}
