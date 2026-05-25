package tool

import (
	"encoding/json"
	"fmt"

	"github.com/ubuildingagent/backend/agentcore"
	"github.com/ubuildingagent/backend/agents"
	"github.com/ubuildingagent/backend/tools/cwd"
)

// ToAgentTool converts a tools.Tool into an agentcore.AgentTool so it can be
// registered with agentcore.Agent / AgentLoopConfig.
//
// Mapping:
//   - Name / Description / InputSchema -> AgentTool fields
//   - ValidateInput  -> AgentTool.ValidateInput hook
//   - CheckPermissions -> AgentTool.CheckPermission hook
//   - IsConcurrencySafe -> AgentTool.ExecutionMode (false = sequential)
//   - Call -> AgentTool.Execute (runs with an empty ToolUseContext derived from loop ctx)
func ToAgentTool(t Tool) agentcore.AgentTool {
	// Serialise InputSchema once; reuse for every LLM context build.
	var params json.RawMessage
	if schema := t.InputSchema(); schema != nil {
		if b, err := json.Marshal(schema); err == nil {
			params = b
		}
	}

	at := agentcore.AgentTool{
		Name:        t.Name(),
		Description: t.Description(nil),
		Parameters:  params,
	}

	// DynamicDescription: recompute from the tool on every call so that
	// tools whose description varies with input (e.g. Bash summarizing the
	// command) always show up-to-date text in the LLM context.
	at.DynamicDescription = func(_ *agentcore.AgentContext) string {
		return t.Description(nil)
	}

	// ExecutionMode: sequential when the tool is not concurrency-safe.
	// We pass nil input here as a conservative default - tools that vary
	// IsConcurrencySafe per-input will be treated as sequential globally.
	if !t.IsConcurrencySafe(nil) {
		at.ExecutionMode = agentcore.ToolExecutionSequential
	}

	// ValidateInput bridge
	at.ValidateInput = func(args json.RawMessage) *agentcore.ToolValidation {
		toolCtx := &agents.ToolUseContext{}
		result := t.ValidateInput(args, toolCtx)
		if result == nil {
			return &agentcore.ToolValidation{Valid: true}
		}
		return &agentcore.ToolValidation{
			Valid:   result.Valid,
			Message: result.Message,
		}
	}

	// CheckPermission bridge
	at.CheckPermission = func(args json.RawMessage) agentcore.ToolPermissionBehavior {
		toolCtx := &agents.ToolUseContext{}
		result, err := t.CheckPermissions(args, toolCtx)
		if err != nil || result == nil {
			return agentcore.ToolPermissionAllow
		}
		switch result.Behavior {
		case PermissionDeny:
			return agentcore.ToolPermissionDeny
		case PermissionAsk:
			return agentcore.ToolPermissionAsk
		default:
			return agentcore.ToolPermissionAllow
		}
	}

	// Execute bridge: run the tool and map ToolResult -> AgentToolResult.
	at.Execute = func(texCtx *agentcore.ToolExecContext) agentcore.AgentToolResult {
		toolCtx := toolUseContextFromExec(texCtx)
		result, err := t.Call(texCtx.Ctx, texCtx.Args, toolCtx)
		if err != nil {
			return agentcore.AgentToolResult{
				Content: fmt.Sprintf("tool %q error: %v", t.Name(), err),
				IsError: true,
			}
		}
		if result == nil {
			return agentcore.AgentToolResult{}
		}

		// Serialise result data to a human-readable string for the LLM.
		content := marshalContent(result.Data)

		var modifier func(*agentcore.AgentContext)
		if result.ContextModifier != nil {
			// Phase 2b: run the ContextModifier and persist any WorkingDirectory
			// change back to the global cwd state so subsequent tools see it.
			modifier = func(_ *agentcore.AgentContext) {
				updated := result.ContextModifier(toolCtx)
				if updated != nil && updated.WorkingDirectory != toolCtx.WorkingDirectory {
					cwd.Set(updated.WorkingDirectory)
				}
			}
		}

		return agentcore.AgentToolResult{
			Content:         content,
			ContextModifier: modifier,
		}
	}

	return at
}

// ToAgentTools converts a slice of tools.Tool into []agentcore.AgentTool.
func ToAgentTools(tools []Tool) []agentcore.AgentTool {
	out := make([]agentcore.AgentTool, 0, len(tools))
	for _, t := range tools {
		if t.IsEnabled() {
			out = append(out, ToAgentTool(t))
		}
	}
	return out
}

// RegistryToAgentTools converts all enabled tools in a Registry into
// []agentcore.AgentTool ready to be passed to agentcore.NewAgent.
// It calls r.GetTools() which already filters out denied/disabled tools.
func RegistryToAgentTools(r *Registry) []agentcore.AgentTool {
	return ToAgentTools(r.GetTools())
}

// toolUseContextFromExec builds an agents.ToolUseContext from the
// agentcore.ToolExecContext, reading the live working directory from the
// global cwd state (Phase 2a fix).
func toolUseContextFromExec(texCtx *agentcore.ToolExecContext) *agents.ToolUseContext {
	dir := cwd.Get()
	tc := &agents.ToolUseContext{
		WorkingDirectory: dir,
		Ctx:              texCtx.Ctx,
	}
	// Phase 4: propagate the OnUpdate callback so tools can stream partial results.
	if texCtx.OnUpdate != nil {
		onUpdate := texCtx.OnUpdate
		tc.OnUpdate = func(partial *agents.PartialToolResult) {
			if partial == nil {
				return
			}
			content := partial.Text
			if content == "" && partial.Data != nil {
				content = marshalContent(partial.Data)
			}
			onUpdate(&agentcore.AgentToolResult{Content: content})
		}
	}
	return tc
}

// marshalContent converts arbitrary tool result data to a string for the LLM.
func marshalContent(data interface{}) string {
	if data == nil {
		return ""
	}
	switch v := data.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Sprintf("%v", v)
		}
		return string(b)
	}
}
