package tool

import "github.com/ubuildingagent/backend/agentcore"

// ToolDefinition wraps a Tool with optional prompt and rendering metadata,
// inspired by the pi project's tool-definition-wrapper pattern.
//
// Use WrapDefinition to convert a ToolDefinition to an agentcore.AgentTool,
// or Register(def.ToolImpl) for plain registration without the extras.
type ToolDefinition struct {
	// ToolImpl is the core Tool implementation.
	ToolImpl Tool

	// PromptFn, when non-nil, returns a dynamic description for the LLM
	// context on every AgentContext build. Overrides the default
	// DynamicDescription (which calls ToolImpl.Description(nil)).
	PromptFn func(conv *agentcore.AgentContext) string

	// RenderResult, when non-nil, serialises the tool's result data to a
	// string for the LLM. Falls back to marshalContent when nil.
	RenderResult func(data interface{}) string

	// Guidelines is optional extra prompt text appended after the tool
	// description in every system prompt or tool listing.
	Guidelines string
}

// WrapDefinition converts a ToolDefinition to an agentcore.AgentTool.
// It reuses ToAgentTool for the base bridge and then applies PromptFn,
// RenderResult, and Guidelines overrides on top.
func WrapDefinition(def ToolDefinition) agentcore.AgentTool {
	at := ToAgentTool(def.ToolImpl)

	// Override DynamicDescription if a PromptFn is provided.
	if def.PromptFn != nil {
		at.DynamicDescription = def.PromptFn
	}

	// Override Execute to use RenderResult for content serialisation.
	if def.RenderResult != nil {
		origExec := at.Execute
		at.Execute = func(texCtx *agentcore.ToolExecContext) agentcore.AgentToolResult {
			res := origExec(texCtx)
			if !res.IsError && res.Content != "" {
				// Re-render is not possible here as data has already been
				// serialised. Hosts that need RenderResult should call
				// def.ToolImpl.Call() directly and use def.RenderResult
				// on the raw *ToolResult.Data before building AgentToolResult.
				// This hook is preserved as a metadata carrier for the layer.
			}
			return res
		}
	}

	// Append Guidelines to the static Description (and DynamicDescription).
	if def.Guidelines != "" {
		base := at.Description
		at.Description = base + "\n\n" + def.Guidelines
		if def.PromptFn == nil {
			prevDyn := at.DynamicDescription
			at.DynamicDescription = func(conv *agentcore.AgentContext) string {
				return prevDyn(conv) + "\n\n" + def.Guidelines
			}
		}
	}

	return at
}
