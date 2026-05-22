//go:build integration

package agentcore

import (
"context"
"encoding/json"
"strings"
"sync/atomic"
"testing"
"time"

"github.com/ubuildingagent/backend/llmprovider"
)

func toolEventsFrom(events []AgentEvent, name string) (starts, ends []AgentEvent) {
for _, ev := range events {
if ev.ToolCall == nil || ev.ToolCall.Name != name {
continue
}
switch ev.Type {
case AgentEventToolStart:
starts = append(starts, ev)
case AgentEventToolEnd:
ends = append(ends, ev)
}
}
return
}

func anyToolEnd(events []AgentEvent) []AgentEvent {
var out []AgentEvent
for _, ev := range events {
if ev.Type == AgentEventToolEnd {
out = append(out, ev)
}
}
return out
}

func userMsg(text string) AgentMessage {
return AgentMessage{
Role:    llmprovider.RoleUser,
Content: []llmprovider.ContentPart{{Type: llmprovider.ContentTypeText, Text: text}},
}
}

func TestToolPipeline_ValidateInput(t *testing.T) {
cfg := loadAgentConfig(t)
var executed atomic.Bool
conv := AgentContext{
SystemPrompt: "You are an assistant. When asked to read a file, call the read_file tool.",
Messages:     []AgentMessage{userMsg("Call read_file but pass an empty JSON object {} as args.")},
Tools: []AgentTool{
{
Name:        "read_file",
Description: "Read a file. Requires a path field.",
Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`),
ValidateInput: func(args json.RawMessage) *ToolValidation {
var in struct {
Path string `json:"path"`
}
if err := json.Unmarshal(args, &in); err != nil || strings.TrimSpace(in.Path) == "" {
return &ToolValidation{Valid: false, Message: "read_file: path is required"}
}
return &ToolValidation{Valid: true}
},
Execute: func(ctx *ToolExecContext) AgentToolResult {
executed.Store(true)
return AgentToolResult{Content: "file content"}
},
},
},
}
ch := RunAgentLoop(context.Background(), cfg, conv)
events := drainLoop(t, ch, 60*time.Second)
ends := anyToolEnd(events)
if len(ends) == 0 {
t.Skip("model did not call read_file")
}
if executed.Load() {
t.Error("T1: Execute should NOT have been called when ValidateInput fails")
}
for _, e := range ends {
if e.ToolCall != nil && e.ToolCall.Result != nil {
t.Logf("T1 tool result: %s", e.ToolCall.Result.Content)
if !strings.Contains(e.ToolCall.Result.Content, "path is required") {
t.Errorf("T1: expected validation error in result, got: %s", e.ToolCall.Result.Content)
}
}
}
t.Logf("T1 final reply: %s", textFromEvents(events))
}

func TestToolPipeline_CheckPermissionDeny(t *testing.T) {
cfg := loadAgentConfig(t)
var executed atomic.Bool
conv := AgentContext{
SystemPrompt: "You are an assistant. When asked to delete a file, call the delete_file tool.",
Messages:     []AgentMessage{userMsg("Call delete_file to remove /tmp/test.txt.")},
Tools: []AgentTool{
{
Name:        "delete_file",
Description: "Delete a file.",
Parameters:  json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}},"required":["path"]}`),
CheckPermission: func(args json.RawMessage) ToolPermissionBehavior {
return ToolPermissionDeny
},
Execute: func(ctx *ToolExecContext) AgentToolResult {
executed.Store(true)
return AgentToolResult{Content: "deleted"}
},
},
},
}
ch := RunAgentLoop(context.Background(), cfg, conv)
events := drainLoop(t, ch, 60*time.Second)
ends := anyToolEnd(events)
if len(ends) == 0 {
t.Skip("model did not call delete_file")
}
if executed.Load() {
t.Error("T2: Execute should NOT have been called when CheckPermission returns Deny")
}
for _, e := range ends {
if e.ToolCall != nil && e.ToolCall.Result != nil {
t.Logf("T2 tool result: %s", e.ToolCall.Result.Content)
if !strings.Contains(e.ToolCall.Result.Content, "permission denied") {
t.Errorf("T2: expected 'permission denied' in result, got: %s", e.ToolCall.Result.Content)
}
}
}
}

func TestToolPipeline_DynamicDescription(t *testing.T) {
cfg := loadAgentConfig(t)
items := []string{}
agent := NewAgent(cfg, "You are an assistant. Call list_items when asked.")
agent.AddTool(AgentTool{
Name:        "list_items",
Description: "List current items.",
Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
DynamicDescription: func(conv *AgentContext) string {
if len(items) == 0 {
return "List current items. Currently 0 items."
}
return "List current items. Items: " + strings.Join(items, ", ")
},
Execute: func(ctx *ToolExecContext) AgentToolResult {
if len(items) == 0 {
return AgentToolResult{Content: "no items"}
}
return AgentToolResult{Content: strings.Join(items, ", ")}
},
})
ch1 := agent.Prompt(context.Background(), "Call list_items to show all items.")
ev1 := drainLoop(t, ch1, 60*time.Second)
starts1, _ := toolEventsFrom(ev1, "list_items")
if len(starts1) == 0 {
t.Skip("T3: model did not call list_items on turn 1")
}
t.Logf("T3 turn1 reply: %s", textFromEvents(ev1))
items = append(items, "apple", "banana", "orange")
ch2 := agent.Prompt(context.Background(), "Call list_items again.")
ev2 := drainLoop(t, ch2, 60*time.Second)
starts2, _ := toolEventsFrom(ev2, "list_items")
if len(starts2) == 0 {
t.Skip("T3: model did not call list_items on turn 2")
}
t.Logf("T3 turn2 reply: %s", textFromEvents(ev2))
}

func TestToolPipeline_ContextModifier(t *testing.T) {
cfg := loadAgentConfig(t)
agent := NewAgent(cfg, "You are an assistant. Call load_context when asked.")
agent.AddTool(AgentTool{
Name:        "load_context",
Description: "Load hidden context into the session.",
Parameters:  json.RawMessage(`{"type":"object","properties":{}}`),
Execute: func(ctx *ToolExecContext) AgentToolResult {
return AgentToolResult{
Content: "context loaded",
ContextModifier: func(conv *AgentContext) {
conv.Messages = append(conv.Messages, AgentMessage{
Role: llmprovider.RoleSystem,
Content: []llmprovider.ContentPart{
{Type: llmprovider.ContentTypeText, Text: "[CONTEXT] SECRET_CODE=XK-9271"},
},
})
},
}
},
})
ch1 := agent.Prompt(context.Background(), "Call load_context.")
ev1 := drainLoop(t, ch1, 60*time.Second)
starts1, _ := toolEventsFrom(ev1, "load_context")
if len(starts1) == 0 {
t.Skip("T4: model did not call load_context")
}
t.Logf("T4 turn1 reply: %s", textFromEvents(ev1))
ch2 := agent.Prompt(context.Background(), "What is SECRET_CODE? Tell me the value directly.")
ev2 := drainLoop(t, ch2, 60*time.Second)
reply2 := textFromEvents(ev2)
t.Logf("T4 turn2 reply: %s", reply2)
if !strings.Contains(reply2, "XK-9271") {
t.Errorf("T4: expected 'XK-9271' in reply, got: %s", reply2)
}
}

func TestToolPipeline_MultiToolRouting(t *testing.T) {
cfg := loadAgentConfig(t)
var calledTools []string
makeExec := func(name, result string) func(*ToolExecContext) AgentToolResult {
return func(ctx *ToolExecContext) AgentToolResult {
calledTools = append(calledTools, name)
return AgentToolResult{Content: result}
}
}
conv := AgentContext{
SystemPrompt: "You are a math assistant. Use the right tool when asked to compute.",
Messages:     []AgentMessage{userMsg("Use a tool to compute 17 + 25.")},
Tools: []AgentTool{
{Name: "add_numbers", Description: "Add two numbers a and b.", Parameters: json.RawMessage(`{"type":"object","properties":{"a":{"type":"number"},"b":{"type":"number"}}}`), Execute: makeExec("add_numbers", "42")},
{Name: "get_time", Description: "Get current time.", Parameters: json.RawMessage(`{"type":"object","properties":{}}`), Execute: makeExec("get_time", "12:00")},
{Name: "echo_text", Description: "Echo input text.", Parameters: json.RawMessage(`{"type":"object","properties":{"text":{"type":"string"}}}`), Execute: makeExec("echo_text", "echo")},
},
}
ch := RunAgentLoop(context.Background(), cfg, conv)
events := drainLoop(t, ch, 60*time.Second)
if len(anyToolEnd(events)) == 0 {
t.Skip("T5: model did not call any tool")
}
t.Logf("T5: tools called = %v", calledTools)
if len(calledTools) == 0 || calledTools[0] != "add_numbers" {
t.Errorf("T5: expected add_numbers to be called, got: %v", calledTools)
}
reply := textFromEvents(events)
t.Logf("T5 final reply: %s", reply)
if !strings.Contains(reply, "42") {
t.Errorf("T5: expected '42' in reply, got: %s", reply)
}
}

func TestToolPipeline_MessageRoundTrip(t *testing.T) {
cfg := loadAgentConfig(t)
conv := AgentContext{
SystemPrompt: "You are a weather assistant. Always call get_weather when asked about weather.",
Messages:     []AgentMessage{userMsg("What is the weather in Beijing? Use the tool.")},
Tools: []AgentTool{
{
Name:        "get_weather",
Description: "Get current weather for a city.",
Parameters:  json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}},"required":["city"]}`),
Execute: func(ctx *ToolExecContext) AgentToolResult {
return AgentToolResult{Content: `{"temperature":"22C","condition":"sunny"}`}
},
},
},
}
ch := RunAgentLoop(context.Background(), cfg, conv)
events := drainLoop(t, ch, 60*time.Second)
ends := anyToolEnd(events)
if len(ends) == 0 {
t.Skip("T6: model did not call get_weather")
}
lastToolEndIdx := -1
for i, ev := range events {
if ev.Type == AgentEventToolEnd {
lastToolEndIdx = i
}
}
hasTextAfterTool := false
for i := lastToolEndIdx + 1; i < len(events); i++ {
if events[i].Type == AgentEventTextDelta {
hasTextAfterTool = true
break
}
}
if !hasTextAfterTool {
t.Error("T6: expected text_delta after tool_end")
}
reply := textFromEvents(events)
t.Logf("T6 final reply: %s", reply)
if reply == "" {
t.Error("T6: expected non-empty final reply")
}
}