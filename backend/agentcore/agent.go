package agentcore

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/ubuildingagent/backend/llmprovider"
)

// AgentState holds the live mutable state of an Agent.
type AgentState struct {
	SystemPrompt     string
	Model            llmprovider.Model
	ThinkingLevel    llmprovider.ThinkingLevel
	Tools            []AgentTool
	Messages         []AgentMessage
	IsStreaming      bool
	StreamingMessage *AgentMessage          // partial assistant message being assembled
	PendingToolCalls []llmprovider.ToolCall // tool calls awaiting execution
	ErrorMessage     string
}

// Agent wraps the agent loop with stateful management.
// It is safe for concurrent reads; Prompt/Continue must be called one at a time.
type Agent struct {
	mu     sync.RWMutex
	state  AgentState
	config AgentLoopConfig
}

// NewAgent creates an Agent with the given config and initial state.
func NewAgent(config AgentLoopConfig, systemPrompt string) *Agent {
	return &Agent{
		config: config,
		state: AgentState{
			SystemPrompt:  systemPrompt,
			Model:         config.Model,
			ThinkingLevel: config.StreamOpts.Reasoning,
		},
	}
}

// State returns a snapshot copy of the current agent state.
func (a *Agent) State() AgentState {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.state
}

// AddTool registers a tool in the agent's current tool list.
func (a *Agent) AddTool(tool AgentTool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.Tools = append(a.state.Tools, tool)
}

// Prompt starts a new conversation turn with the given user message.
// Returns a channel of AgentEvents that the caller must drain.
func (a *Agent) Prompt(ctx context.Context, userText string) <-chan AgentEvent {
	userMsg := AgentMessage{
		ID:        newID(),
		Role:      llmprovider.RoleUser,
		Content:   []llmprovider.ContentPart{{Type: llmprovider.ContentTypeText, Text: userText}},
		Source:    "user",
		Timestamp: time.Now(),
	}

	a.mu.Lock()
	a.state.Messages = append(a.state.Messages, userMsg)
	conv := a.buildConv()
	a.mu.Unlock()

	return a.runAndTrack(ctx, conv)
}

// Continue resumes the loop without adding a new user message.
// Useful after injecting messages directly into state.
func (a *Agent) Continue(ctx context.Context) <-chan AgentEvent {
	a.mu.RLock()
	conv := a.buildConv()
	a.mu.RUnlock()
	return a.runAndTrack(ctx, conv)
}

// AppendMessage adds a message to the history without triggering a loop turn.
func (a *Agent) AppendMessage(msg AgentMessage) {
	if msg.Timestamp.IsZero() {
		msg.Timestamp = time.Now()
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.state.Messages = append(a.state.Messages, msg)
}

// buildConv builds an AgentContext from the current state.
// Must be called with at least a read lock held.
func (a *Agent) buildConv() AgentContext {
	msgs := make([]AgentMessage, len(a.state.Messages))
	copy(msgs, a.state.Messages)
	tools := make([]AgentTool, len(a.state.Tools))
	copy(tools, a.state.Tools)

	cfg := a.config
	cfg.Model = a.state.Model
	cfg.StreamOpts.Reasoning = a.state.ThinkingLevel

	return AgentContext{
		SystemPrompt: a.state.SystemPrompt,
		Messages:     msgs,
		Tools:        tools,
	}
}

// runAndTrack runs the loop and subscribes to events to keep state current.
func (a *Agent) runAndTrack(ctx context.Context, conv AgentContext) <-chan AgentEvent {
	outCh := make(chan AgentEvent, 64)
	loopCh := RunAgentLoop(ctx, a.config, conv)

	a.mu.Lock()
	a.state.IsStreaming = true
	a.state.PendingToolCalls = nil
	a.state.ErrorMessage = ""
	a.mu.Unlock()

	go func() {
		defer close(outCh)
		defer func() {
			a.mu.Lock()
			a.state.IsStreaming = false
			a.state.StreamingMessage = nil
			a.state.PendingToolCalls = nil
			a.mu.Unlock()
		}()

		var textAcc, thinkingAcc string
		var pendingTCs []llmprovider.ToolCall

		for ev := range loopCh {
			// ── Update live state ──────────────────────────────────────
			switch ev.Type {
			case AgentEventTextDelta:
				textAcc += ev.Delta
				a.mu.Lock()
				if a.state.StreamingMessage == nil {
					a.state.StreamingMessage = &AgentMessage{Role: llmprovider.RoleAssistant}
				}
				a.state.StreamingMessage.Content = []llmprovider.ContentPart{
					{Type: llmprovider.ContentTypeText, Text: textAcc},
				}
				a.mu.Unlock()

			case AgentEventThinkingDelta:
				thinkingAcc += ev.Delta

			case AgentEventToolStart:
				if ev.ToolCall != nil {
					pendingTCs = append(pendingTCs, llmprovider.ToolCall{
						ID:   ev.ToolCall.ID,
						Name: ev.ToolCall.Name,
					})
					a.mu.Lock()
					a.state.PendingToolCalls = pendingTCs
					a.mu.Unlock()
				}

			case AgentEventToolEnd:
				if ev.ToolCall != nil {
					newPending := pendingTCs[:0]
					for _, tc := range pendingTCs {
						if tc.ID != ev.ToolCall.ID {
							newPending = append(newPending, tc)
						}
					}
					pendingTCs = newPending
					a.mu.Lock()
					a.state.PendingToolCalls = pendingTCs
					a.mu.Unlock()
				}

			case AgentEventTurnEnd:
				// Persist the completed assistant message
				if ev.Message != nil {
					a.mu.Lock()
					// Replace last assistant message if it already exists (from streaming)
					// or append if new
					a.state.Messages = append(a.state.Messages, *ev.Message)
					a.state.StreamingMessage = nil
					a.mu.Unlock()
				}
				textAcc = ""
				thinkingAcc = ""

			case AgentEventError:
				if ev.Err != nil {
					a.mu.Lock()
					a.state.ErrorMessage = ev.Err.Error()
					a.mu.Unlock()
				}
			}

			// Forward to caller
			select {
			case outCh <- ev:
			case <-ctx.Done():
				return
			}
		}
	}()

	return outCh
}

// newID generates a simple timestamp-based ID for messages.
func newID() string {
	return fmt.Sprintf("msg_%d", time.Now().UnixNano())
}
