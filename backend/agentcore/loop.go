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
			if ctx.Err() != nil {
				// Context cancelled: keep original error event for abort scenarios.
				emit(ch, AgentEvent{Type: AgentEventError, Err: ctx.Err()})
				return
			}
			// Non-cancellation fatal error: synthesise an error assistant message
			// so consumers receive a well-formed TurnEnd+AgentEnd event sequence.
			errMsg := AgentMessage{
				ID:           newID(),
				Role:         llmprovider.RoleAssistant,
				IsError:      true,
				ErrorMessage: err.Error(),
				Source:       "assistant",
				Timestamp:    time.Now(),
			}
			conv.Messages = append(conv.Messages, errMsg)
			emit(ch, AgentEvent{Type: AgentEventTurnEnd, Message: &errMsg})
			break
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

		// ── Memory: inject recalled context before LLM call ───────────────
		// RecallContext is called with the text of the last user message as
		// the query.  The result is prepended as a Hidden system message so
		// the model can leverage relevant memories without modifying history.
		if cfg.Memory != nil {
			query := lastUserText(conv.Messages)
			if recalled := cfg.Memory.RecallContext(ctx, query); recalled != "" {
				recallMsg := llmprovider.Message{
					Role:    llmprovider.RoleSystem,
					Content: []llmprovider.ContentPart{{Type: llmprovider.ContentTypeText, Text: recalled}},
				}
				llmCtx.Messages = append([]llmprovider.Message{recallMsg}, llmCtx.Messages...)
			}
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

		// Record cost and output-token consumption in budget
		if cfg.Budget != nil && usage != nil {
			if usage.CostUSD > 0 {
				_ = cfg.Budget.Consume(0, usage.CostUSD)
			}
			if usage.OutputTokens > 0 {
				_ = cfg.Budget.ConsumeOutputTokens(usage.OutputTokens)
			}
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

		toolResults, toolMods, terminate, err := executeTools(ctx, cfg, conv, assistantMsg, ch)
		if err != nil {
			return false, err
		}

		// Append tool result messages first — this establishes the correct OpenAI
		// message ordering: assistant(tool_calls) → tool(result) → [context patches].
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

		// Apply ContextModifiers AFTER tool results are appended so patches land
		// in the correct position: ...tool_result → context_patches.
		for _, tm := range toolMods {
			if tm.modifier == nil {
				continue
			}
			before := len(conv.Messages)
			tm.modifier(conv)
			if added := conv.Messages[before:]; len(added) > 0 {
				patch := make([]AgentMessage, len(added))
				copy(patch, added)
				emit(ch, AgentEvent{Type: AgentEventContextPatch, Messages: patch})
			}
		}

		// ── Memory: async sync turn after tool results ────────────────────
		// SyncTurn is called in a goroutine to avoid blocking the main loop.
		// It persists the completed user→assistant→tools turn for future recall.
		if cfg.Memory != nil && len(conv.Messages) >= 2 {
			userMsg, asstMsg := lastTurnMessages(conv.Messages)
			go cfg.Memory.SyncTurn(ctx, userMsg, asstMsg)
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
//
// P1-4: in addition to the legacy text/thinking delta events, this function now
// emits message_start (on first content), message_update (on every delta), and
// message_end (after final assembly), each carrying the current partial message.
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

	// P1-4: partial message lifecycle tracking
	partialStarted := false

	// buildPartialTCs assembles in-progress tool calls for a partial snapshot.
	buildPartialTCs := func() []llmprovider.ToolCall {
		tcs := make([]llmprovider.ToolCall, 0, len(toolCallsInProgress))
		for i := 0; i < len(toolCallsInProgress)+10; i++ {
			acc, ok := toolCallsInProgress[i]
			if !ok {
				continue
			}
			argsStr := acc.argsBuilder.String()
			if argsStr == "" {
				argsStr = "{}"
			}
			tcs = append(tcs, llmprovider.ToolCall{
				ID: acc.id, Name: acc.name,
				Arguments: json.RawMessage(argsStr),
			})
		}
		return tcs
	}

	// emitPartialUpdate emits message_start on the first call, message_update
	// on subsequent calls — both carrying the current partial assembled message.
	emitPartialUpdate := func() {
		partial := AgentMessageFromLLMEvent(textBuilder.String(), thinkingBuilder.String(), buildPartialTCs())
		evType := AgentEventMessageUpdate
		if !partialStarted {
			partialStarted = true
			evType = AgentEventMessageStart
		}
		emit(ch, AgentEvent{Type: evType, Message: &partial})
	}

	for ev := range streamCh {
		switch ev.Type {
		case llmprovider.StreamEventError:
			return AgentMessage{}, nil, fmt.Errorf("llm stream error: %w", ev.Err)

		case llmprovider.StreamEventTextDelta:
			textBuilder.WriteString(ev.Delta)
			emit(ch, AgentEvent{Type: AgentEventTextDelta, Delta: ev.Delta})
			emitPartialUpdate()

		case llmprovider.StreamEventThinkingDelta:
			thinkingBuilder.WriteString(ev.Delta)
			emit(ch, AgentEvent{Type: AgentEventThinkingDelta, Delta: ev.Delta})
			emitPartialUpdate()

		case llmprovider.StreamEventToolCallStart:
			if ev.ToolCall != nil {
				toolCallsInProgress[ev.ToolCall.Index] = &toolCallAccumulator{
					id:   ev.ToolCall.ID,
					name: ev.ToolCall.Name,
				}
				emitPartialUpdate()
			}

		case llmprovider.StreamEventToolCallDelta:
			if ev.ToolCall != nil {
				if acc, ok := toolCallsInProgress[ev.ToolCall.Index]; ok {
					acc.argsBuilder.WriteString(ev.ToolCall.ArgsDelta)
					emitPartialUpdate()
				}
			}

		case llmprovider.StreamEventToolCallEnd:
			// The provider sends the fully-accumulated args string in ArgsDelta on
			// End (not just the final delta).  Reset and replace so that consumers
			// of both Delta and End events don't double-count the content.
			if ev.ToolCall != nil {
				if acc, ok := toolCallsInProgress[ev.ToolCall.Index]; ok {
					if ev.ToolCall.ArgsDelta != "" {
						acc.argsBuilder.Reset()
						acc.argsBuilder.WriteString(ev.ToolCall.ArgsDelta)
					}
					emitPartialUpdate()
				}
			}

		case llmprovider.StreamEventMessageEnd:
			finalUsage = ev.Usage
		}
	}

	// Assemble final tool calls (deterministic index order)
	toolCalls := make([]llmprovider.ToolCall, 0, len(toolCallsInProgress))
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

	// P1-4: emit message_end with the final assembled message
	if partialStarted {
		msgCopy := msg
		emit(ch, AgentEvent{Type: AgentEventMessageEnd, Message: &msgCopy})
	}

	return msg, finalUsage, nil
}

// toolCallAccumulator collects streaming tool call deltas.
type toolCallAccumulator struct {
	id          string
	name        string
	argsBuilder strings.Builder
}

// effectiveModeForCall returns the execution mode for a single tool call.
// The tool's own ExecutionMode field overrides the global config; falls back
// to globalMode when the tool has no explicit preference.
func effectiveModeForCall(tc llmprovider.ToolCall, conv *AgentContext, globalMode ToolExecutionMode) ToolExecutionMode {
	for i := range conv.Tools {
		if conv.Tools[i].Name == tc.Name && conv.Tools[i].ExecutionMode != "" {
			return conv.Tools[i].ExecutionMode
		}
	}
	return globalMode
}

// executeTools runs all tool calls from an assistant message.
// Returns (toolResultMessages, terminate, error).
//
// Execution mode resolution (highest priority first):
//  1. Individual AgentTool.ExecutionMode override
//  2. AgentLoopConfig.ToolExecution global setting
//  3. Default: sequential
//
// If global mode is parallel but any tool explicitly requests sequential,
// the entire batch falls back to sequential to preserve source ordering.
// toolModifier pairs a tool result message with its deferred ContextModifier.
type toolModifier struct {
	modifier func(*AgentContext) // nil if none
}

func executeTools(
	ctx context.Context,
	cfg AgentLoopConfig,
	conv *AgentContext,
	assistantMsg AgentMessage,
	ch chan<- AgentEvent,
) ([]AgentMessage, []toolModifier, bool, error) {
	mode := cfg.ToolExecution
	if mode == "" {
		mode = ToolExecutionSequential
	}

	if mode == ToolExecutionParallel && len(assistantMsg.ToolCalls) > 1 {
		// Per-tool override: if any tool explicitly requires sequential execution,
		// fall back to sequential for the whole batch to preserve source ordering.
		allParallel := true
		for _, tc := range assistantMsg.ToolCalls {
			if effectiveModeForCall(tc, conv, mode) == ToolExecutionSequential {
				allParallel = false
				break
			}
		}
		if allParallel {
			return executeToolsParallel(ctx, cfg, conv, assistantMsg, ch)
		}
	}
	return executeToolsSequential(ctx, cfg, conv, assistantMsg, ch)
}

func executeToolsSequential(
	ctx context.Context,
	cfg AgentLoopConfig,
	conv *AgentContext,
	assistantMsg AgentMessage,
	ch chan<- AgentEvent,
) ([]AgentMessage, []toolModifier, bool, error) {
	var results []AgentMessage
	var modifiers []toolModifier
	terminate := false

	for _, tc := range assistantMsg.ToolCalls {
		msg, mod, term, err := executeSingleTool(ctx, cfg, conv, assistantMsg, tc, ch)
		if err != nil {
			return nil, nil, false, err
		}
		results = append(results, msg)
		modifiers = append(modifiers, mod)
		if term {
			terminate = true
		}
	}
	return results, modifiers, terminate, nil
}

func executeToolsParallel(
	ctx context.Context,
	cfg AgentLoopConfig,
	conv *AgentContext,
	assistantMsg AgentMessage,
	ch chan<- AgentEvent,
) ([]AgentMessage, []toolModifier, bool, error) {
	type result struct {
		idx       int
		msg       AgentMessage
		mod       toolModifier
		terminate bool
		err       error
	}

	resultCh := make(chan result, len(assistantMsg.ToolCalls))
	var wg sync.WaitGroup

	for i, tc := range assistantMsg.ToolCalls {
		wg.Add(1)
		go func(idx int, tc llmprovider.ToolCall) {
			defer wg.Done()
			msg, mod, term, err := executeSingleTool(ctx, cfg, conv, assistantMsg, tc, ch)
			resultCh <- result{idx: idx, msg: msg, mod: mod, terminate: term, err: err}
		}(i, tc)
	}

	wg.Wait()
	close(resultCh)

	// Collect results in source order
	ordered := make([]result, len(assistantMsg.ToolCalls))
	for r := range resultCh {
		if r.err != nil {
			return nil, nil, false, r.err
		}
		ordered[r.idx] = r
	}

	msgs := make([]AgentMessage, 0, len(ordered))
	mods := make([]toolModifier, 0, len(ordered))
	terminate := false
	for _, r := range ordered {
		msgs = append(msgs, r.msg)
		mods = append(mods, r.mod)
		if r.terminate {
			terminate = true
		}
	}
	return msgs, mods, terminate, nil
}

func executeSingleTool(
	ctx context.Context,
	cfg AgentLoopConfig,
	conv *AgentContext,
	assistantMsg AgentMessage,
	tc llmprovider.ToolCall,
	ch chan<- AgentEvent,
) (AgentMessage, toolModifier, bool, error) {
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
			return ToolResultMessage(tc.ID, blocked), toolModifier{}, false, nil
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
		// PrepareArguments hook — normalise raw LLM args before validation (P0-3)
		if found.PrepareArguments != nil {
			if normalized := found.PrepareArguments(tc.Arguments); normalized != nil {
				tc.Arguments = normalized
			}
		}

		// ValidateInput hook — abort early on invalid args
		if found.ValidateInput != nil {
			if v := found.ValidateInput(tc.Arguments); v != nil && !v.Valid {
				msg := v.Message
				if msg == "" {
					msg = fmt.Sprintf("tool %q: invalid input", tc.Name)
				}
				toolResult = AgentToolResult{Content: msg, IsError: true}
				isError = true
				goto afterExec
			}
		}

		// CheckPermission hook — deny or delegate to BeforeToolCall
		if found.CheckPermission != nil {
			switch found.CheckPermission(tc.Arguments) {
			case ToolPermissionDeny:
				toolResult = AgentToolResult{
					Content: fmt.Sprintf("tool %q: permission denied", tc.Name),
					IsError: true,
				}
				isError = true
				goto afterExec
			}
			// ToolPermissionAllow and ToolPermissionAsk both fall through;
			// "ask" defers the decision to the existing BeforeToolCall hook.
		}

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
				Ctx:      ctx,
				Call:     tc,
				Args:     tc.Arguments,
				AgentCtx: conv,
				OnUpdate: func(partial *AgentToolResult) {
					emit(ch, AgentEvent{
						Type: AgentEventToolUpdate,
						ToolCall: &ToolCallEvent{
							ID:            tc.ID,
							Name:          tc.Name,
							Arguments:     tc.Arguments,
							PartialResult: partial,
						},
					})
				},
			}
			toolResult = found.Execute(texCtx)
			isError = toolResult.IsError
		}()
	}
afterExec:

	// AfterToolCall hook
	_ = found // suppress potential goto-skip warning
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

	// ContextModifier is deferred: returned to the caller so it can be applied
	// AFTER all tool result messages have been appended to conv.  This ensures
	// the correct OpenAI message order: assistant(tool_calls) → tool(result) →
	// [context patches].  Applying it here would insert patches BEFORE the
	// tool result, causing HTTP 400 on strict endpoints.
	return ToolResultMessage(tc.ID, toolResult), toolModifier{modifier: toolResult.ContextModifier}, toolResult.Terminate, nil
}

// lastUserText returns the text content of the most-recent user message in
// the conversation, used as the recall query for the MemoryProvider.
func lastUserText(msgs []AgentMessage) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == llmprovider.RoleUser {
			for _, p := range msgs[i].Content {
				if p.Type == llmprovider.ContentTypeText && p.Text != "" {
					return p.Text
				}
			}
		}
	}
	return ""
}

// lastTurnMessages returns the last user message and the last assistant
// message from the conversation for SyncTurn.  When the conversation is
// shorter than expected the zero-value AgentMessage is returned safely.
func lastTurnMessages(msgs []AgentMessage) (userMsg, assistantMsg AgentMessage) {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == llmprovider.RoleAssistant && assistantMsg.ID == "" {
			assistantMsg = msgs[i]
		}
		if msgs[i].Role == llmprovider.RoleUser && userMsg.ID == "" {
			userMsg = msgs[i]
		}
		if userMsg.ID != "" && assistantMsg.ID != "" {
			break
		}
	}
	return userMsg, assistantMsg
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
