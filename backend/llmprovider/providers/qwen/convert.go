// Package qwen implements the DashScope native API provider for Qwen models.
// This is distinct from openaicompat.Provider: the DashScope native endpoint
// uses a different URL path, SSE header (X-DashScope-SSE), and response format.
package qwen

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/ubuildingagent/backend/llmprovider"
)

// convertMessages converts internal messages to DashScope chat format.
// DashScope uses the same structure as OpenAI chat except for content parts
// where video content uses DashScope-specific {"type":"video",...} format.
func convertMessages(messages []llmprovider.Message, systemPrompt string) []map[string]any {
	out := make([]map[string]any, 0, len(messages)+1)
	if systemPrompt != "" {
		out = append(out, map[string]any{"role": "system", "content": systemPrompt})
	}
	for _, msg := range messages {
		switch msg.Role {
		case llmprovider.RoleUser:
			content := convertContentParts(msg.Content)
			if len(content) == 1 {
				if t, ok := content[0].(map[string]any); ok && t["type"] == "text" {
					out = append(out, map[string]any{"role": "user", "content": t["text"]})
					continue
				}
			}
			out = append(out, map[string]any{"role": "user", "content": content})
		case llmprovider.RoleAssistant:
			m := map[string]any{"role": "assistant"}
			if len(msg.ToolCalls) > 0 {
				m["content"] = ""
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
				text := ""
				for _, p := range msg.Content {
					if p.Type == llmprovider.ContentTypeText {
						text += p.Text
					}
				}
				m["content"] = text
			}
			out = append(out, m)
		case llmprovider.RoleTool:
			var toolCallID, result string
			for _, p := range msg.Content {
				if p.Type == llmprovider.ContentTypeToolResult {
					toolCallID = p.ToolCallID
					result = p.ToolResult
				} else if p.Type == llmprovider.ContentTypeText {
					result += p.Text
				}
			}
			out = append(out, map[string]any{
				"role":         "tool",
				"tool_call_id": toolCallID,
				"content":      result,
			})
		}
	}
	return out
}

// convertContentParts converts ContentParts to DashScope format.
// Key difference from OpenAI: video content uses {"type":"video","video":"url"}
// or {"type":"video","video":["b64frame1","b64frame2",...]}
func convertContentParts(parts []llmprovider.ContentPart) []any {
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
			// DashScope native: {"type":"video","video":"<url>","fps":2.0}
			v := map[string]any{"type": "video", "video": p.VideoURL}
			if p.VideoFPS > 0 {
				v["fps"] = p.VideoFPS
			}
			if p.VideoNFrames > 0 {
				v["nframes"] = p.VideoNFrames
			}
			result = append(result, v)
		case llmprovider.ContentTypeVideoFrame:
			if llmprovider.IsVideoFrameGroup(p) {
				// Sentinel: consume next VideoNFrames as base64 array
				nf := p.VideoNFrames
				frames := make([]string, 0, nf)
				for j := 0; j < nf && i+1+j < len(parts); j++ {
					f := parts[i+1+j]
					frames = append(frames, base64.StdEncoding.EncodeToString(f.Data))
				}
				result = append(result, map[string]any{"type": "video", "video": frames})
				i += nf
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
		}
		i++
	}
	return result
}

// convertTools converts Tool definitions to DashScope format (same as OpenAI).
func convertTools(tools []llmprovider.Tool) []map[string]any {
	if len(tools) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		var params any
		if len(t.Parameters) > 0 {
			_ = json.Unmarshal(t.Parameters, &params)
		}
		out = append(out, map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name,
				"description": t.Description,
				"parameters":  params,
			},
		})
	}
	return out
}
