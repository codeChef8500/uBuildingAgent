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

	// P1-6: abort + idle notification
	cancelMu sync.Mutex
	cancelFn context.CancelFunc // non-nil while a run is active
	idleMu   sync.Mutex
	idleCh   chan struct{} // closed when current run ends; replaced on next run

	// P2-7: multi-cast event listeners
	listenersMu    sync.RWMutex
	listeners      map[int]func(AgentEvent)
	nextListenerID int

	// P2-8: explicit message queues
	queueMu       sync.Mutex
	steeringQueue MessageQueue
	followUpQueue MessageQueue
}

// NewAgent creates an Agent with the given config and initial state.
func NewAgent(config AgentLoopConfig, systemPrompt string) *Agent {
	a := &Agent{
		config: config,
		state: AgentState{
			SystemPrompt:  systemPrompt,
			Model:         config.Model,
			ThinkingLevel: config.StreamOpts.Reasoning,
		},
		listeners:     make(map[int]func(AgentEvent)),
		steeringQueue: MessageQueue{Mode: QueueModeOneAtATime},
		followUpQueue: MessageQueue{Mode: QueueModeOneAtATime},
	}
	a.idleCh = closedChan() // starts idle
	return a
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
	conv, cfg := a.buildConvAndConfig()
	a.mu.Unlock()

	return a.runAndTrack(ctx, cfg, conv)
}

// Continue resumes the loop without adding a new user message.
//
// P0-2: returns an error event immediately if the last message in history is
// from the assistant — continuing in that state would create an invalid
// assistant → assistant message sequence.
func (a *Agent) Continue(ctx context.Context) <-chan AgentEvent {
	a.mu.RLock()
	msgs := a.state.Messages
	lastIsAssistant := len(msgs) > 0 && msgs[len(msgs)-1].Role == llmprovider.RoleAssistant
	conv, cfg := a.buildConvAndConfig()
	a.mu.RUnlock()

	if lastIsAssistant {
		ch := make(chan AgentEvent, 1)
		ch <- AgentEvent{
			Type: AgentEventError,
			Err:  fmt.Errorf("agentcore: Continue called when last message is assistant role"),
		}
		close(ch)
		return ch
	}
	return a.runAndTrack(ctx, cfg, conv)
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

// ── Queue API (P2-8) ──────────────────────────────────────────────────────

// Steer enqueues a message to be injected as a steering message at the start
// of the next inner-loop iteration (before the next LLM call).
func (a *Agent) Steer(msg AgentMessage) {
	if msg.Timestamp.IsZero() {
		msg.Timestamp = time.Now()
	}
	a.queueMu.Lock()
	defer a.queueMu.Unlock()
	a.steeringQueue.Enqueue(msg)
}

// FollowUp enqueues a message that triggers a new outer-loop iteration after
// the current inner loop completes.
func (a *Agent) FollowUp(msg AgentMessage) {
	if msg.Timestamp.IsZero() {
		msg.Timestamp = time.Now()
	}
	a.queueMu.Lock()
	defer a.queueMu.Unlock()
	a.followUpQueue.Enqueue(msg)
}

// ClearQueues removes all pending steering and follow-up messages.
func (a *Agent) ClearQueues() {
	a.queueMu.Lock()
	defer a.queueMu.Unlock()
	a.steeringQueue.Clear()
	a.followUpQueue.Clear()
}

// ── Subscription API (P2-7) ───────────────────────────────────────────────

// Subscribe registers fn to be called synchronously for every AgentEvent
// emitted during a run (in addition to being forwarded on the returned channel).
// Returns an unsubscribe function; calling it deregisters fn.
func (a *Agent) Subscribe(fn func(AgentEvent)) (unsubscribe func()) {
	a.listenersMu.Lock()
	id := a.nextListenerID
	a.nextListenerID++
	a.listeners[id] = fn
	a.listenersMu.Unlock()

	return func() {
		a.listenersMu.Lock()
		delete(a.listeners, id)
		a.listenersMu.Unlock()
	}
}

// ── Lifecycle API (P1-6) ──────────────────────────────────────────────────

// Abort cancels the currently running agent loop.  No-op if the agent is idle.
func (a *Agent) Abort() {
	a.cancelMu.Lock()
	defer a.cancelMu.Unlock()
	if a.cancelFn != nil {
		a.cancelFn()
	}
}

// WaitForIdle returns a channel that is closed when the agent becomes idle
// (i.e. the current run finishes or is aborted).  If the agent is already
// idle the returned channel is already closed.
func (a *Agent) WaitForIdle() <-chan struct{} {
	a.idleMu.Lock()
	defer a.idleMu.Unlock()
	return a.idleCh
}

// AbortAndWait cancels the current run and blocks until idle or ctx expires.
func (a *Agent) AbortAndWait(ctx context.Context) error {
	a.Abort()
	select {
	case <-a.WaitForIdle():
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// ── Internal ──────────────────────────────────────────────────────────────

// buildConvAndConfig builds the AgentContext and a run-local AgentLoopConfig
// (with state overrides and queue wiring applied).
// Must be called with at least a read lock held.
func (a *Agent) buildConvAndConfig() (AgentContext, AgentLoopConfig) {
	msgs := make([]AgentMessage, len(a.state.Messages))
	copy(msgs, a.state.Messages)
	tools := make([]AgentTool, len(a.state.Tools))
	copy(tools, a.state.Tools)

	// Apply per-state overrides to a local copy
	cfg := a.config
	cfg.Model = a.state.Model
	cfg.StreamOpts.Reasoning = a.state.ThinkingLevel

	// P2-8: compose queue drain with any existing user-supplied callbacks
	userSteer := cfg.GetSteeringMessages
	cfg.GetSteeringMessages = func(msg AgentMessage) []AgentMessage {
		a.queueMu.Lock()
		queued := a.steeringQueue.Drain()
		a.queueMu.Unlock()
		if userSteer != nil {
			queued = append(queued, userSteer(msg)...)
		}
		return queued
	}

	userFollowUp := cfg.GetFollowUpMessages
	cfg.GetFollowUpMessages = func() []AgentMessage {
		a.queueMu.Lock()
		queued := a.followUpQueue.Drain()
		a.queueMu.Unlock()
		if userFollowUp != nil {
			queued = append(queued, userFollowUp()...)
		}
		return queued
	}

	conv := AgentContext{
		SystemPrompt: a.state.SystemPrompt,
		Messages:     msgs,
		Tools:        tools,
	}
	return conv, cfg
}

// runAndTrack runs the loop and subscribes to events to keep state current.
func (a *Agent) runAndTrack(ctx context.Context, cfg AgentLoopConfig, conv AgentContext) <-chan AgentEvent {
	// P1-6: wrap context for abort support
	runCtx, cancel := context.WithCancel(ctx)
	a.cancelMu.Lock()
	a.cancelFn = cancel
	a.cancelMu.Unlock()

	// P1-6: create a fresh idle channel for this run
	idleCh := make(chan struct{})
	a.idleMu.Lock()
	a.idleCh = idleCh
	a.idleMu.Unlock()

	outCh := make(chan AgentEvent, 64)
	loopCh := RunAgentLoop(runCtx, cfg, conv)

	a.mu.Lock()
	a.state.IsStreaming = true
	a.state.PendingToolCalls = nil
	a.state.ErrorMessage = ""
	a.mu.Unlock()

	go func() {
		defer close(outCh)
		defer cancel() // release resources if caller didn't abort explicitly
		defer func() {
			a.mu.Lock()
			a.state.IsStreaming = false
			a.state.StreamingMessage = nil
			a.state.PendingToolCalls = nil
			a.mu.Unlock()

			// P1-6: signal idle to WaitForIdle waiters
			close(idleCh)
			a.cancelMu.Lock()
			a.cancelFn = nil
			a.cancelMu.Unlock()
		}()

		var pendingTCs []llmprovider.ToolCall

		for ev := range loopCh {
			// ── Update live state ──────────────────────────────────────
			switch ev.Type {
			// P1-4: MessageStart/Update carry the live partial assembled by loop.go;
			// use it directly instead of rebuilding from raw text deltas.
			case AgentEventMessageStart, AgentEventMessageUpdate:
				if ev.Message != nil {
					msgCopy := *ev.Message
					a.mu.Lock()
					a.state.StreamingMessage = &msgCopy
					a.mu.Unlock()
				}

			case AgentEventMessageEnd:
				// Final message will be committed at TurnEnd; just clear partial here.
				a.mu.Lock()
				a.state.StreamingMessage = nil
				a.mu.Unlock()

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
					// Persist tool result so multi-turn history is valid:
					// assistant(tool_calls) → tool_result → assistant(text).
					if ev.ToolCall.Result != nil {
						a.state.Messages = append(a.state.Messages,
							ToolResultMessage(ev.ToolCall.ID, *ev.ToolCall.Result))
					}
					a.mu.Unlock()
				}

			case AgentEventContextPatch:
				// Persist messages injected by ContextModifier so they appear
				// in history for subsequent Prompt() calls.
				if len(ev.Messages) > 0 {
					a.mu.Lock()
					a.state.Messages = append(a.state.Messages, ev.Messages...)
					a.mu.Unlock()
				}

			case AgentEventTurnEnd:
				if ev.Message != nil {
					a.mu.Lock()
					a.state.Messages = append(a.state.Messages, *ev.Message)
					a.state.StreamingMessage = nil
					// P0-1: propagate error detail from synthetic error messages
					if ev.Message.IsError && ev.Message.ErrorMessage != "" {
						a.state.ErrorMessage = ev.Message.ErrorMessage
					}
					a.mu.Unlock()
				}

			case AgentEventError:
				if ev.Err != nil {
					a.mu.Lock()
					a.state.ErrorMessage = ev.Err.Error()
					a.mu.Unlock()
				}
			}

			// P2-7: dispatch to registered listeners before forwarding to caller
			a.listenersMu.RLock()
			for _, fn := range a.listeners {
				fn(ev)
			}
			a.listenersMu.RUnlock()

			// Forward to caller
			select {
			case outCh <- ev:
			case <-runCtx.Done():
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

// closedChan returns an already-closed channel, used as the initial idle
// sentinel for a newly created Agent.
func closedChan() chan struct{} {
	ch := make(chan struct{})
	close(ch)
	return ch
}
