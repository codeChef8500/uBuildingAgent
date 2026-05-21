package agentcore

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/ubuildingagent/backend/llmprovider"
)

// RunAgentLoop starts the agent loop and returns a channel of AgentEvents.
//
// Architecture:
//
//	Outer loop: driven by GetFollowUpMessages; each iteration adds a new user
//	            message and runs the inner loop.
//	Inner loop: LLM call → collect streaming events → execute tools (seq or
//	            parallel) → optional steering messages → next LLM call.
//	            Exits when LLM returns no tool calls, ShouldStopAfterTurn=true,
//	            or budget is exhausted.
//
// The channel is closed when the loop ends (normally or on error).
func RunAgentLoop(
	ctx context.Context,
	config AgentLoopConfig,
	conv AgentContext,
) <-chan AgentEvent {
	ch := make(chan AgentEvent, 64)
	go func() {
		defer close(ch)
		runLoop(ctx, config, &conv, ch)
	}()
	return ch
}

// ── Internal loop driver ──────────────────────────────────────────────────

func runLoop(
	ctx context.Context,
	cfg AgentLoopConfig,
	conv *AgentContext,
	ch chan<- AgentEvent,
) {
	emit(ch, AgentEvent{Type: AgentEventStart})

	// Outer loop: follow-up messages
	for {
		if ctx.Err() != nil {
			emit(ch, AgentEvent{Type: AgentEventError, Err: ctx.Err()})
			return
		}

		// Inner loop: LLM ↔ Tools
		stop, err := runInnerLoop(ctx, cfg, conv, ch)
		if err != nil {
			emit(ch, AgentEvent{Type: AgentEventError, Err: err})
			return
		}
		if stop {
			break
		}

		// Outer follow-up
		if cfg.GetFollowUpMessages == nil {
			break
		}
		followUps := cfg.GetFollowUpMessages()
		if len(followUps) == 0 {
			break
		}
		for _, fm := range followUps {
			fm.Timestamp = time.Now()
			conv.Messages = append(conv.Messages, fm)
		}
	}

	emit(ch, AgentEvent{Type: AgentEventEnd})
}

// runInnerLoop executes one outer turn (potentially many LLM+tool cycles).
// Returns (stop=true, nil) on graceful stop; (false, err) on fatal error.
func runInnerLoop(
	ctx context.Context,
	cfg AgentLoopConfig,
	conv *AgentContext,
	ch chan<- AgentEvent,
) (stop bool, err error) {
	for {
		if ctx.Err() != nil {
			return false, ctx.Err()
		}

		// ── Budget check ──────────────────────────────────────────────────
		if cfg.Budget != nil {
			if err := cfg.Budget.Consume(1, 0); err != nil {
				return false, err
			}
		}

		// ── PrepareNextTurn ───────────────────────────────────────────────
		model := cfg.Model
		thinkingLevel := cfg.StreamOpts.Reasoning
		if cfg.PrepareNextTurn != nil {
			update := &AgentLoopTurnUpdate{
				AgentCtx:      conv,
				Model:         &model,
				ThinkingLevel: &thinkingLevel,
			}
			cfg.PrepareNextTurn(update)
		}

		// ── Context compaction (T4-7) ─────────────────────────────────────
		if cfg.ContextEngine != nil && cfg.CompactionSettings.MaxTokens > 0 {
			tokens := cfg.ContextEngine.EstimateContextTokens(conv.Messages)
			if cfg.ContextEngine.ShouldCompact(tokens, cfg.CompactionSettings.MaxTokens) {
				compacted, compactErr := cfg.ContextEngine.CompactMessages(conv.Messages, cfg.CompactionSettings.MaxTokens)
				if compactErr == nil {
					conv.Messages = compacted
				}
				// persist compaction entry if session is set
				if cfg.Session != nil {
					_ = cfg.Session.AppendEntry(map[string]any{
						"type":        "compaction",
						"pre_tokens":  tokens,
						"post_tokens": cfg.ContextEngine.EstimateContextTokens(conv.Messages),
					})
				}
			}
		}

		// ── Build LLM context ─────────────────────────────────────────────
		llmCtx := conv.ToLLMContext(cfg.ConvertToLLM)
		if cfg.TransformContext != nil {
			llmCtx = cfg.TransformContext(llmCtx)
		}

		// ── Stream opts ───────────────────────────────────────────────────
		streamOpts := cfg.StreamOpts
		streamOpts.Reasoning = thinkingLevel
		if cfg.GetAPIKey != nil {
			streamOpts.APIKey = cfg.GetAPIKey()
		}

		// ── LLM call ──────────────────────────────────────────────────────
		emit(ch, AgentEvent{Type: AgentEventTurnStart})

		assistantMsg, usage, loopErr := collectLLMTurn(ctx, model, llmCtx, streamOpts, ch)
		if loopErr != nil {
			return false, loopErr
		}

		// Record cost in budget
		if cfg.Budget != nil && usage != nil && usage.CostUSD > 0 {
			_ = cfg.Budget.Consume(0, usage.CostUSD)
		}

		assistantMsg.Timestamp = time.Now()
		conv.Messages = append(conv.Messages, assistantMsg)

		// Persist assistant message to session
		if cfg.Session != nil {
			_ = cfg.Session.AppendEntry(map[string]any{
				"type":    "message",
				"message": assistantMsg,
			})
		}

		emit(ch, AgentEvent{
			Type:    AgentEventTurnEnd,
			Message: &assistantMsg,
			Usage:   usage,
		})

		// ── Tool execution ────────────────────────────────────────────────
		if len(assistantMsg.ToolCalls) == 0 {
			// No tools: ask whether to stop
			if cfg.ShouldStopAfterTurn != nil {
				sctx := &ShouldStopContext{
					AssistantMessage: assistantMsg,
					AgentCtx:         conv,
					NewMessages:      []AgentMessage{assistantMsg},
				}
				if cfg.ShouldStopAfterTurn(sctx) {
					return true, nil
				}
			}
			return true, nil // default: stop when no tools
		}

		toolResults, terminate, err := executeTools(ctx, cfg, conv, assistantMsg, ch)
		if err != nil {
			return false, err
		}

		// Append tool result messages
		for _, tr := range toolResults {
			tr.Timestamp = time.Now()
			conv.Messages = append(conv.Messages, tr)
			if cfg.Session != nil {
				_ = cfg.Session.AppendEntry(map[string]any{
					"type":    "tool_result",
					"message": tr,
				})
			}
		}

		// Steering messages (injected after tool results, before next LLM call)
		if cfg.GetSteeringMessages != nil {
			for _, sm := range cfg.GetSteeringMessages(assistantMsg) {
				sm.Timestamp = time.Now()
				conv.Messages = append(conv.Messages, sm)
			}
		}

		// ShouldStopAfterTurn
		if cfg.ShouldStopAfterTurn != nil {
			results := make([]AgentToolResult, 0, len(toolResults))
			for range toolResults {
				results = append(results, AgentToolResult{}) // placeholder
			}
			sctx := &ShouldStopContext{
				AssistantMessage: assistantMsg,
				ToolResults:      results,
				AgentCtx:         conv,
				NewMessages:      append([]AgentMessage{assistantMsg}, toolResults...),
			}
			if cfg.ShouldStopAfterTurn(sctx) {
				return true, nil
			}
		}

		if terminate {
			return true, nil
		}
	}
}

// collectLLMTurn streams a single LLM call, emitting AgentEvents, and returns
// the assembled assistant message plus final usage.
func collectLLMTurn(
	ctx context.Context,
	model llmprovider.Model,
	llmCtx llmprovider.Context,
	opts llmprovider.SimpleStreamOptions,
	ch chan<- AgentEvent,
) (AgentMessage, *llmprovider.Usage, error) {
	streamCh := llmprovider.StreamSimple(ctx, model, llmCtx, opts)

	var textBuilder strings.Builder
	var thinkingBuilder strings.Builder
	var toolCallsInProgress = map[int]*toolCallAccumulator{}
	var finalUsage *llmprovider.Usage

	for ev := range streamCh {
		switch ev.Type {
		case llmprovider.StreamEventError:
			return AgentMessage{}, nil, fmt.Errorf("llm stream error: %w", ev.Err)

		case llmprovider.StreamEventTextDelta:
			textBuilder.WriteString(ev.Delta)
			emit(ch, AgentEvent{Type: AgentEventTextDelta, Delta: ev.Delta})

		case llmprovider.StreamEventThinkingDelta:
			thinkingBuilder.WriteString(ev.Delta)
			emit(ch, AgentEvent{Type: AgentEventThinkingDelta, Delta: ev.Delta})

		case llmprovider.StreamEventToolCallStart:
			if ev.ToolCall != nil {
				toolCallsInProgress[ev.ToolCall.Index] = &toolCallAccumulator{
					id:   ev.ToolCall.ID,
					name: ev.ToolCall.Name,
				}
			}

		case llmprovider.StreamEventToolCallDelta:
			if ev.ToolCall != nil {
				if acc, ok := toolCallsInProgress[ev.ToolCall.Index]; ok {
					acc.argsBuilder.WriteString(ev.ToolCall.ArgsDelta)
				}
			}

		case llmprovider.StreamEventToolCallEnd:
			if ev.ToolCall != nil {
				if acc, ok := toolCallsInProgress[ev.ToolCall.Index]; ok {
					acc.argsBuilder.WriteString(ev.ToolCall.ArgsDelta)
				}
			}

		case llmprovider.StreamEventMessageEnd:
			finalUsage = ev.Usage
		}
	}

	// Assemble tool calls
	toolCalls := make([]llmprovider.ToolCall, 0, len(toolCallsInProgress))
	// sort by index for deterministic order
	for i := 0; i < len(toolCallsInProgress)+10; i++ {
		acc, ok := toolCallsInProgress[i]
		if !ok {
			continue
		}
		argsStr := acc.argsBuilder.String()
		if argsStr == "" {
			argsStr = "{}"
		}
		toolCalls = append(toolCalls, llmprovider.ToolCall{
			ID:        acc.id,
			Name:      acc.name,
			Arguments: json.RawMessage(argsStr),
		})
	}

	msg := AgentMessageFromLLMEvent(textBuilder.String(), thinkingBuilder.String(), toolCalls)
	return msg, finalUsage, nil
}

// toolCallAccumulator collects streaming tool call deltas.
type toolCallAccumulator struct {
	id          string
	name        string
	argsBuilder strings.Builder
}

// executeTools runs all tool calls from an assistant message.
// Returns (toolResultMessages, terminate, error).
func executeTools(
	ctx context.Context,
	cfg AgentLoopConfig,
	conv *AgentContext,
	assistantMsg AgentMessage,
	ch chan<- AgentEvent,
) ([]AgentMessage, bool, error) {
	mode := cfg.ToolExecution
	if mode == "" {
		mode = ToolExecutionSequential
	}

	if mode == ToolExecutionParallel && len(assistantMsg.ToolCalls) > 1 {
		return executeToolsParallel(ctx, cfg, conv, assistantMsg, ch)
	}
	return executeToolsSequential(ctx, cfg, conv, assistantMsg, ch)
}

func executeToolsSequential(
	ctx context.Context,
	cfg AgentLoopConfig,
	conv *AgentContext,
	assistantMsg AgentMessage,
	ch chan<- AgentEvent,
) ([]AgentMessage, bool, error) {
	var results []AgentMessage
	terminate := false

	for _, tc := range assistantMsg.ToolCalls {
		msg, term, err := executeSingleTool(ctx, cfg, conv, assistantMsg, tc, ch)
		if err != nil {
			return nil, false, err
		}
		results = append(results, msg)
		if term {
			terminate = true
		}
	}
	return results, terminate, nil
}

func executeToolsParallel(
	ctx context.Context,
	cfg AgentLoopConfig,
	conv *AgentContext,
	assistantMsg AgentMessage,
	ch chan<- AgentEvent,
) ([]AgentMessage, bool, error) {
	type result struct {
		idx       int
		msg       AgentMessage
		terminate bool
		err       error
	}

	resultCh := make(chan result, len(assistantMsg.ToolCalls))
	var wg sync.WaitGroup

	for i, tc := range assistantMsg.ToolCalls {
		wg.Add(1)
		go func(idx int, tc llmprovider.ToolCall) {
			defer wg.Done()
			msg, term, err := executeSingleTool(ctx, cfg, conv, assistantMsg, tc, ch)
			resultCh <- result{idx: idx, msg: msg, terminate: term, err: err}
		}(i, tc)
	}

	wg.Wait()
	close(resultCh)

	// Collect results in source order
	ordered := make([]result, len(assistantMsg.ToolCalls))
	for r := range resultCh {
		if r.err != nil {
			return nil, false, r.err
		}
		ordered[r.idx] = r
	}

	msgs := make([]AgentMessage, 0, len(ordered))
	terminate := false
	for _, r := range ordered {
		msgs = append(msgs, r.msg)
		if r.terminate {
			terminate = true
		}
	}
	return msgs, terminate, nil
}

func executeSingleTool(
	ctx context.Context,
	cfg AgentLoopConfig,
	conv *AgentContext,
	assistantMsg AgentMessage,
	tc llmprovider.ToolCall,
	ch chan<- AgentEvent,
) (AgentMessage, bool, error) {
	// BeforeToolCall hook
	if cfg.BeforeToolCall != nil {
		bctx := &BeforeToolCallContext{
			AssistantMessage: assistantMsg,
			ToolCallID:       tc.ID,
			ToolName:         tc.Name,
			Args:             tc.Arguments,
			AgentCtx:         conv,
		}
		res := cfg.BeforeToolCall(bctx)
		if res.Block {
			blocked := AgentToolResult{
				Content: fmt.Sprintf("[blocked: %s]", res.Reason),
				IsError: true,
			}
			emit(ch, AgentEvent{
				Type:     AgentEventToolStart,
				ToolCall: &ToolCallEvent{ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments},
			})
			emit(ch, AgentEvent{
				Type: AgentEventToolEnd,
				ToolCall: &ToolCallEvent{
					ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments,
					Result: &blocked, IsError: true,
				},
			})
			return ToolResultMessage(tc.ID, blocked), false, nil
		}
	}

	// Find tool
	toolResult := AgentToolResult{}
	isError := false

	emit(ch, AgentEvent{
		Type:     AgentEventToolStart,
		ToolCall: &ToolCallEvent{ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments},
	})

	var found *AgentTool
	for i := range conv.Tools {
		if conv.Tools[i].Name == tc.Name {
			found = &conv.Tools[i]
			break
		}
	}

	if found == nil || found.Execute == nil {
		toolResult = AgentToolResult{
			Content: fmt.Sprintf("tool %q not found or not executable", tc.Name),
			IsError: true,
		}
		isError = true
	} else {
		func() {
			defer func() {
				if r := recover(); r != nil {
					toolResult = AgentToolResult{
						Content: fmt.Sprintf("tool %q panicked: %v", tc.Name, r),
						IsError: true,
					}
					isError = true
				}
			}()
			texCtx := &ToolExecContext{
				Call:     tc,
				Args:     tc.Arguments,
				AgentCtx: conv,
			}
			toolResult = found.Execute(texCtx)
			isError = toolResult.IsError
		}()
	}

	// AfterToolCall hook
	if cfg.AfterToolCall != nil {
		actx := &AfterToolCallContext{
			AssistantMessage: assistantMsg,
			ToolCallID:       tc.ID,
			ToolName:         tc.Name,
			Args:             tc.Arguments,
			Result:           toolResult,
			IsError:          isError,
			AgentCtx:         conv,
		}
		patch := cfg.AfterToolCall(actx)
		toolResult = patch.Apply(toolResult)
		isError = toolResult.IsError
	}

	emit(ch, AgentEvent{
		Type: AgentEventToolEnd,
		ToolCall: &ToolCallEvent{
			ID: tc.ID, Name: tc.Name, Arguments: tc.Arguments,
			Result: &toolResult, IsError: isError,
		},
	})

	return ToolResultMessage(tc.ID, toolResult), toolResult.Terminate, nil
}

// emit sends an event non-blockingly (drops if channel is full, which only
// happens if caller is not draining — should not happen in normal use).
func emit(ch chan<- AgentEvent, ev AgentEvent) {
	select {
	case ch <- ev:
	default:
		// channel full: try blocking send to preserve ordering guarantees
		ch <- ev
	}
}
