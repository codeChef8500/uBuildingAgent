package agentcore

import (
	"github.com/ubuildingagent/backend/llmprovider"
)

// DefaultConvertToLLM converts AgentMessages to llmprovider.Messages.
// Rules:
//   - Messages with Hidden=true are excluded from the LLM context.
//   - Source="system" messages are excluded by default (handled via SystemPrompt).
//   - Tool calls and thinking fields are mapped 1-to-1.
func DefaultConvertToLLM(messages []AgentMessage) []llmprovider.Message {
	out := make([]llmprovider.Message, 0, len(messages))
	for _, m := range messages {
		if m.Hidden {
			continue
		}
		if m.Source == "system" {
			continue
		}
		out = append(out, agentMsgToLLM(m))
	}
	return out
}

// DefaultConvertToLLMIncludeSystem is like DefaultConvertToLLM but also
// includes Source="system" messages (converted to role "system").
func DefaultConvertToLLMIncludeSystem(messages []AgentMessage) []llmprovider.Message {
	out := make([]llmprovider.Message, 0, len(messages))
	for _, m := range messages {
		if m.Hidden {
			continue
		}
		out = append(out, agentMsgToLLM(m))
	}
	return out
}

func agentMsgToLLM(m AgentMessage) llmprovider.Message {
	return llmprovider.Message{
		Role:      m.Role,
		Content:   m.Content,
		ToolCalls: m.ToolCalls,
		Thinking:  m.Thinking,
	}
}

// AgentMessageFromLLMEvent builds an assistant AgentMessage from streaming
// deltas collected during a single LLM turn.
func AgentMessageFromLLMEvent(
	textContent string,
	thinking string,
	toolCalls []llmprovider.ToolCall,
) AgentMessage {
	content := []llmprovider.ContentPart{}
	if textContent != "" {
		content = append(content, llmprovider.ContentPart{
			Type: llmprovider.ContentTypeText,
			Text: textContent,
		})
	}
	return AgentMessage{
		Role:      llmprovider.RoleAssistant,
		Content:   content,
		ToolCalls: toolCalls,
		Thinking:  thinking,
		Source:    "assistant",
	}
}

// ToolResultMessage builds a tool-result AgentMessage from an AgentToolResult.
func ToolResultMessage(toolCallID string, result AgentToolResult) AgentMessage {
	return AgentMessage{
		Role: llmprovider.RoleTool,
		Content: []llmprovider.ContentPart{
			{
				Type:       llmprovider.ContentTypeToolResult,
				ToolCallID: toolCallID,
				ToolResult: result.Content,
			},
		},
		Source: "tool",
	}
}
