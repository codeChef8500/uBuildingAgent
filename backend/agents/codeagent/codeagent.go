// Package codeagent demonstrates how to assemble a fully-wired coding agent
// using agentcore, the tools registry, memory, and skills.
//
// This is an example / reference implementation, not production code.
// Copy, rename, and extend it to build concrete agents.
package codeagent

import (
"github.com/ubuildingagent/backend/agentcore"
"github.com/ubuildingagent/backend/llmprovider"
"github.com/ubuildingagent/backend/memroy"
"github.com/ubuildingagent/backend/skills"
tool "github.com/ubuildingagent/backend/tools"
)

// Config holds the options for building a CodeAgent.
type Config struct {
// APIKey for the LLM provider.
APIKey string

// Model to use (e.g. Claude 3.5 Sonnet).
Model llmprovider.Model

// Registry is the pre-built tool registry; nil uses an empty registry.
Registry *tool.Registry

// Memory is an optional memory provider; nil disables memory integration.
Memory agentcore.MemoryProvider

// Skills is an optional skills provider; nil disables skills integration.
Skills agentcore.SkillProvider

// SystemPrompt is the base system prompt.  The agent layer is responsible
// for assembling the full prompt -- agentcore does NOT build it.
SystemPrompt string
}

// New builds and returns an agentcore.Agent wired with tools, memory, and skills.
//
// Assembly order:
//  1. Convert registry tools -> agentcore.AgentTool slice.
//  2. Append memory tool schemas (read_memory, write_memory) if Memory != nil.
//  3. Append skills tool schemas (list_skills, invoke_skill) if Skills != nil.
//  4. Build AgentLoopConfig with the Memory / Skills providers.
//  5. Construct system prompt (base + optional memory/skills blocks).
//  6. Return a fully-wired Agent.
func New(cfg Config) *agentcore.Agent {
// 1. Base tools from registry
var agentTools []agentcore.AgentTool
if cfg.Registry != nil {
agentTools = tool.RegistryToAgentTools(cfg.Registry)
}

// 2. Memory tool schemas
if cfg.Memory != nil {
agentTools = append(agentTools, cfg.Memory.ToolSchemas()...)
}

// 3. Skills tool schemas
if cfg.Skills != nil {
agentTools = append(agentTools, cfg.Skills.ToolSchemas()...)
}

// 4. Build system prompt (agent layer responsibility)
systemPrompt := buildSystemPrompt(cfg)

// 5. AgentLoopConfig
loopCfg := agentcore.AgentLoopConfig{
Model: cfg.Model,
StreamOpts: llmprovider.SimpleStreamOptions{
StreamOptions: llmprovider.StreamOptions{APIKey: cfg.APIKey},
},
ToolExecution: agentcore.ToolExecutionSequential,
Memory:        cfg.Memory,
Skills:        cfg.Skills,
}

// 6. Construct agent
agent := agentcore.NewAgent(loopCfg, systemPrompt)
for _, t := range agentTools {
agent.AddTool(t)
}
return agent
}

// buildSystemPrompt assembles the full system prompt from the base prompt and
// optional memory / skills blocks.  System prompt design belongs here, not in
// agentcore.
func buildSystemPrompt(cfg Config) string {
prompt := cfg.SystemPrompt

if mp, ok := cfg.Memory.(*memroy.Provider); ok {
if block := mp.SystemPromptBlock(); block != "" {
prompt += "\n\n" + block
}
}

if sp, ok := cfg.Skills.(*skills.Provider); ok {
if block := sp.SystemPromptBlock(); block != "" {
prompt += "\n\n" + block
}
}

return prompt
}
