// Package skills provides the SkillProvider implementation for agentcore.
//
// Design overview:
//   - Implements agentcore.SkillProvider interface.
//   - Skills are Markdown files (with optional YAML frontmatter) stored on disk
//     or in a database.  Each skill has a name, description, and prompt body.
//   - ListSkills returns lightweight metadata for DynamicDescription generation.
//   - LoadSkill returns the full prompt content for injection into the conversation.
//   - ToolSchemas exposes list_skills and invoke_skill AgentTools to the LLM.
//
// NOTE: This is a skeleton.  Fill in the loader to read from disk, DB, or a
// remote registry.
package skills

import (
"encoding/json"
"fmt"
"strings"

"github.com/ubuildingagent/backend/agentcore"
"github.com/ubuildingagent/backend/llmprovider"
)

// Provider is the concrete skills backend satisfying agentcore.SkillProvider.
type Provider struct {
skills map[string]Skill
}

// Skill holds the full definition of a single skill.
type Skill struct {
Meta    agentcore.SkillMeta
Content string // Markdown prompt body
}

// NewProvider creates a Provider with a pre-loaded skill set.
func NewProvider(skills []Skill) *Provider {
p := &Provider{skills: make(map[string]Skill, len(skills))}
for _, s := range skills {
p.skills[s.Meta.Name] = s
}
return p
}

// ListSkills implements agentcore.SkillProvider.
func (p *Provider) ListSkills() []agentcore.SkillMeta {
out := make([]agentcore.SkillMeta, 0, len(p.skills))
for _, s := range p.skills {
out = append(out, s.Meta)
}
return out
}

// LoadSkill implements agentcore.SkillProvider.
func (p *Provider) LoadSkill(name string) (string, error) {
s, ok := p.skills[name]
if !ok {
return "", fmt.Errorf("skill %q not found", name)
}
return s.Content, nil
}

// ToolSchemas implements agentcore.SkillProvider.
// Returns two AgentTools: list_skills and invoke_skill.
func (p *Provider) ToolSchemas() []agentcore.AgentTool {
return []agentcore.AgentTool{
p.listSkillsTool(),
p.invokeSkillTool(),
}
}

// SystemPromptBlock returns a brief note about available skills for use in
// system prompts.  Called by the concrete agent layer -- NOT by agentcore.
func (p *Provider) SystemPromptBlock() string {
names := make([]string, 0, len(p.skills))
for n := range p.skills {
names = append(names, n)
}
if len(names) == 0 {
return ""
}
return "Available skills: " + strings.Join(names, ", ") +
". Use the invoke_skill tool to run a skill."
}

// -- AgentTool builders -------------------------------------------------------

func (p *Provider) listSkillsTool() agentcore.AgentTool {
return agentcore.AgentTool{
Name:        "list_skills",
Description: "List all available skills with their descriptions.",
Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
DynamicDescription: func(conv *agentcore.AgentContext) string {
metas := p.ListSkills()
if len(metas) == 0 {
return "List available skills. No skills are currently loaded."
}
var sb strings.Builder
sb.WriteString("List available skills. Currently loaded:\n")
for _, m := range metas {
sb.WriteString(fmt.Sprintf("- %s: %s\n", m.Name, m.Description))
}
return sb.String()
},
Execute: func(texCtx *agentcore.ToolExecContext) agentcore.AgentToolResult {
metas := p.ListSkills()
if len(metas) == 0 {
return agentcore.AgentToolResult{Content: "No skills available."}
}
var sb strings.Builder
for _, m := range metas {
sb.WriteString(fmt.Sprintf("- %s: %s\n", m.Name, m.Description))
}
return agentcore.AgentToolResult{Content: sb.String()}
},
}
}

func (p *Provider) invokeSkillTool() agentcore.AgentTool {
paramsSchema := json.RawMessage(`{
"type": "object",
"properties": {
"name": {
"type": "string",
"description": "The name of the skill to invoke."
}
},
"required": ["name"]
}`)

return agentcore.AgentTool{
Name:          "invoke_skill",
Description:   "Invoke a skill by name to get its prompt content injected into the conversation.",
Parameters:    paramsSchema,
ExecutionMode: agentcore.ToolExecutionSequential,

ValidateInput: func(args json.RawMessage) *agentcore.ToolValidation {
var input struct {
Name string `json:"name"`
}
if err := json.Unmarshal(args, &input); err != nil || input.Name == "" {
return &agentcore.ToolValidation{Valid: false, Message: "invoke_skill requires a non-empty \"name\" field"}
}
return &agentcore.ToolValidation{Valid: true}
},

Execute: func(texCtx *agentcore.ToolExecContext) agentcore.AgentToolResult {
var input struct {
Name string `json:"name"`
}
if err := json.Unmarshal(texCtx.Args, &input); err != nil {
return agentcore.AgentToolResult{
Content: "invalid arguments for invoke_skill",
IsError: true,
}
}
content, err := p.LoadSkill(input.Name)
if err != nil {
return agentcore.AgentToolResult{
Content: err.Error(),
IsError: true,
}
}
// Inject the skill content as a non-hidden system message so the LLM
// sees it on the next turn.  Not using Hidden=true because
// DefaultConvertToLLM excludes hidden messages from LLM context.
return agentcore.AgentToolResult{
Content: fmt.Sprintf("Skill %q loaded.", input.Name),
ContextModifier: func(conv *agentcore.AgentContext) {
conv.Messages = append(conv.Messages, agentcore.AgentMessage{
Role: llmprovider.RoleSystem,
Content: []llmprovider.ContentPart{
{Type: llmprovider.ContentTypeText, Text: content},
},
})
},
}
},
}
}
