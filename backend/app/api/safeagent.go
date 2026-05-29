// Package api exposes HTTP endpoints for the SafeAgent system.
package api

import (
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/ubuildingagent/backend/agentcore"
	"github.com/ubuildingagent/backend/agents/safeagent"
)

// HandleInspect handles POST /api/safeagent/inspect.
//
// Request body (JSON):
//
//	{
//	  "scene_description": "高空作业现场，发现工人未系安全绳",
//	  "image_url":         "https://example.com/frame.jpg",   // optional
//	  "location":          "3号楼东侧脚手架"                    // optional
//	}
//
// Response: text/event-stream — each line is "data: <AgentEvent JSON>\n\n".
// The stream ends when the orchestrator agent loop completes.
func HandleInspect(cfg safeagent.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		var input safeagent.SceneInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, fmt.Sprintf("invalid request body: %v", err), http.StatusBadRequest)
			return
		}
		if input.Description == "" {
			http.Error(w, "scene_description is required", http.StatusBadRequest)
			return
		}

		// Set SSE headers before writing any body.
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}

		// Build orchestrator and encode input as the initial prompt.
		orchestrator := safeagent.New(cfg)
		prompt, _ := json.Marshal(input)

		ch := orchestrator.Prompt(r.Context(), string(prompt))
		for ev := range ch {
			b, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			fmt.Fprintf(w, "data: %s\n\n", b)
			flusher.Flush()

			// Stop forwarding on error but drain the channel.
			if ev.Type == agentcore.AgentEventError {
				break
			}
		}
		// Drain remaining events to avoid goroutine leak.
		for range ch {
		}
	}
}
