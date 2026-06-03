package safeagent

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/ubuildingagent/backend/agentcore"
)

// VideoEventType identifies the kind of event in a video inspection SSE stream.
type VideoEventType string

const (
	VideoEventFrameStart  VideoEventType = "frame_start"
	VideoEventFrameDrop   VideoEventType = "frame_drop"
	VideoEventFrameDone   VideoEventType = "frame_done"
	VideoEventQueueStatus VideoEventType = "queue_status"
	VideoEventTextDelta   VideoEventType = "text_delta"
	VideoEventThinking    VideoEventType = "thinking"
	VideoEventToolStart   VideoEventType = "tool_start"
	VideoEventToolEnd     VideoEventType = "tool_end"
	VideoEventError       VideoEventType = "error"
	VideoEventSessionEnd  VideoEventType = "session_end"
)

// VideoEvent is a single event pushed to the frontend over SSE.
type VideoEvent struct {
	Type       VideoEventType  `json:"type"`
	FrameIdx   int             `json:"frame_idx"`
	Timestamp  float64         `json:"timestamp"` // seconds into the video
	Delta      string          `json:"delta,omitempty"`
	ToolName   string          `json:"tool_name,omitempty"`
	ToolArgs   json.RawMessage `json:"tool_args,omitempty"`
	ToolResult string          `json:"tool_result,omitempty"`
	Report     json.RawMessage `json:"report,omitempty"` // InspectionContext JSON
	ErrMsg     string          `json:"err,omitempty"`
	RunningIdx int             `json:"running_idx"` // -1 = none
	PendingIdx int             `json:"pending_idx"` // -1 = none
}

// FrameJob represents one video frame to inspect.
type FrameJob struct {
	Idx       int
	Timestamp float64 // seconds into the video
	ImageURL  string  // URL accessible by the pipeline
	Desc      string  // scene description from the user
}

// VideoSession manages a sliding-window inspection session.
//
// Policy: at most 1 running + 1 pending frame. New frames overwrite
// the pending slot so only the most recent frame is ever enqueued.
type VideoSession struct {
	cfg Config

	mu        sync.Mutex
	running   *FrameJob
	pending   *FrameJob
	cancelRun context.CancelFunc

	eventCh   chan VideoEvent
	closeCh   chan struct{}
	closeOnce sync.Once
	wg        sync.WaitGroup // counts active runFrame goroutines
	history   []FrameJob     // history of submitted frames
}

// NewVideoSession creates an idle VideoSession.
func NewVideoSession(cfg Config) *VideoSession {
	return &VideoSession{
		cfg:     cfg,
		eventCh: make(chan VideoEvent, 256),
		closeCh: make(chan struct{}),
	}
}

// Events returns the read-only event channel.
// It is closed after Stop() is called and any in-flight frame finishes.
func (s *VideoSession) Events() <-chan VideoEvent {
	return s.eventCh
}

// Submit applies the sliding-window policy to enqueue a frame for inspection.
func (s *VideoSession) Submit(job FrameJob) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.isClosed() {
		return
	}

	// Keep history of submitted frames for sliding-window multi-image context
	s.history = append(s.history, job)
	if len(s.history) > 100 {
		s.history = s.history[len(s.history)-100:]
	}

	if s.running == nil {
		s.startLocked(job)
		return
	}

	// A job is running — replace the pending slot with the newest frame.
	if s.pending != nil {
		s.safeSend(VideoEvent{
			Type:       VideoEventFrameDrop,
			FrameIdx:   s.pending.Idx,
			Timestamp:  s.pending.Timestamp,
			RunningIdx: s.running.Idx,
			PendingIdx: job.Idx,
		})
	}
	s.pending = &job
	s.safeSend(VideoEvent{
		Type:       VideoEventQueueStatus,
		RunningIdx: s.running.Idx,
		PendingIdx: job.Idx,
	})
}

// Stop cancels the current job and closes the event channel after cleanup.
func (s *VideoSession) Stop() {
	s.closeOnce.Do(func() {
		s.mu.Lock()
		if s.cancelRun != nil {
			s.cancelRun()
		}
		s.mu.Unlock()

		close(s.closeCh)

		// Close eventCh once all goroutines have exited.
		go func() {
			s.wg.Wait()
			close(s.eventCh)
		}()
	})
}

// isClosed reports whether Stop has been called. Caller must hold s.mu.
func (s *VideoSession) isClosed() bool {
	select {
	case <-s.closeCh:
		return true
	default:
		return false
	}
}

// startLocked launches a goroutine to process job. Caller must hold s.mu.
func (s *VideoSession) startLocked(job FrameJob) {
	ctx, cancel := context.WithCancel(context.Background())
	s.cancelRun = cancel
	s.running = &job

	s.safeSend(VideoEvent{
		Type:       VideoEventFrameStart,
		FrameIdx:   job.Idx,
		Timestamp:  job.Timestamp,
		RunningIdx: job.Idx,
		PendingIdx: -1,
	})

	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.runFrame(ctx, job)
	}()
}

// getFrameWindow returns the URLs of the current and up to windowSize previous frames.
func (s *VideoSession) getFrameWindow(currentIdx int, windowSize int) []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	var matches []FrameJob
	for _, f := range s.history {
		if f.Idx <= currentIdx {
			matches = append(matches, f)
		}
	}

	if len(matches) > windowSize {
		matches = matches[len(matches)-windowSize:]
	}

	urls := make([]string, len(matches))
	for i, f := range matches {
		urls[i] = f.ImageURL
	}
	return urls
}

// runFrame executes the full 3-agent pipeline and forwards events.
func (s *VideoSession) runFrame(ctx context.Context, job FrameJob) {
	// Retrieve a sliding window of the last 3 frames (current + 2 previous).
	// With 2s frame interval, this spans ~6s of video time.
	imageURLs := s.getFrameWindow(job.Idx, 3)

	input := SceneInput{
		Description: job.Desc,
		ImageURL:    job.ImageURL, // Keep for backward-compatibility
		ImageURLs:   imageURLs,
	}
	prompt, _ := json.Marshal(input)

	orchestrator := New(s.cfg)
	ch := orchestrator.Prompt(ctx, string(prompt))

	var fullText strings.Builder
	cancelled := false

	for ev := range ch {
		// Check if session was stopped.
		select {
		case <-s.closeCh:
			cancelled = true
			for range ch {
			} // drain to unblock the agent goroutine
			goto done
		default:
		}

		pendingIdx := s.getPendingIdx()

		switch ev.Type {
		case agentcore.AgentEventTextDelta:
			fullText.WriteString(ev.Delta)
			s.safeSend(VideoEvent{
				Type:       VideoEventTextDelta,
				FrameIdx:   job.Idx,
				Timestamp:  job.Timestamp,
				Delta:      ev.Delta,
				RunningIdx: job.Idx,
				PendingIdx: pendingIdx,
			})

		case agentcore.AgentEventThinkingDelta:
			s.safeSend(VideoEvent{
				Type:       VideoEventThinking,
				FrameIdx:   job.Idx,
				Timestamp:  job.Timestamp,
				Delta:      ev.Delta,
				RunningIdx: job.Idx,
				PendingIdx: pendingIdx,
			})

		case agentcore.AgentEventToolStart:
			if ev.ToolCall != nil {
				s.safeSend(VideoEvent{
					Type:       VideoEventToolStart,
					FrameIdx:   job.Idx,
					Timestamp:  job.Timestamp,
					ToolName:   ev.ToolCall.Name,
					ToolArgs:   ev.ToolCall.Arguments,
					RunningIdx: job.Idx,
					PendingIdx: pendingIdx,
				})
			}

		case agentcore.AgentEventToolEnd:
			if ev.ToolCall != nil {
				result := ""
				if ev.ToolCall.Result != nil {
					result = ev.ToolCall.Result.Content
				}
				s.safeSend(VideoEvent{
					Type:       VideoEventToolEnd,
					FrameIdx:   job.Idx,
					Timestamp:  job.Timestamp,
					ToolName:   ev.ToolCall.Name,
					ToolResult: result,
					RunningIdx: job.Idx,
					PendingIdx: pendingIdx,
				})
			}

		case agentcore.AgentEventError:
			errMsg := ""
			if ev.Err != nil {
				errMsg = ev.Err.Error()
			}
			s.safeSend(VideoEvent{
				Type:       VideoEventError,
				FrameIdx:   job.Idx,
				Timestamp:  job.Timestamp,
				ErrMsg:     errMsg,
				RunningIdx: job.Idx,
				PendingIdx: pendingIdx,
			})
		}
	}

done:
	s.finishFrame(job, fullText.String(), cancelled)
}

// finishFrame emits frame_done and starts any pending job.
func (s *VideoSession) finishFrame(job FrameJob, fullText string, cancelled bool) {
	var report json.RawMessage
	if raw := extractInspectionJSON(fullText); raw != nil {
		report = raw
	}

	if !cancelled {
		s.safeSend(VideoEvent{
			Type:       VideoEventFrameDone,
			FrameIdx:   job.Idx,
			Timestamp:  job.Timestamp,
			Report:     report,
			RunningIdx: -1,
			PendingIdx: s.getPendingIdx(),
		})
	}

	s.mu.Lock()
	s.running = nil
	s.cancelRun = nil
	next := s.pending
	s.pending = nil
	closed := s.isClosed()
	if next != nil && !closed {
		s.startLocked(*next)
	}
	s.mu.Unlock()
}

func (s *VideoSession) getPendingIdx() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pending == nil {
		return -1
	}
	return s.pending.Idx
}

func (s *VideoSession) safeSend(ev VideoEvent) {
	select {
	case s.eventCh <- ev:
	default: // drop if the consumer is slow; non-critical telemetry
	}
}

// extractInspectionJSON finds the first valid JSON object starting with {"input"
// in the orchestrator's full text output (the InspectionContext summary).
func extractInspectionJSON(text string) json.RawMessage {
	start := strings.Index(text, `{"input"`)
	if start < 0 {
		return nil
	}
	sub := text[start:]
	depth, end := 0, -1
	for i, c := range sub {
		switch c {
		case '{':
			depth++
		case '}':
			depth--
			if depth == 0 {
				end = i
			}
		}
		if end >= 0 {
			break
		}
	}
	if end < 0 {
		return nil
	}
	raw := []byte(sub[:end+1])
	var v json.RawMessage
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil
	}
	return v
}
