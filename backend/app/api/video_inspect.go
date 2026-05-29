package api

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/ubuildingagent/backend/agents/safeagent"
)

// ── Session Manager ────────────────────────────────────────────────────────

type videoSessionManager struct {
	mu       sync.RWMutex
	sessions map[string]*safeagent.VideoSession
	cfg      safeagent.Config
	frameDir string // temp directory for uploaded JPEG frames
}

func newVideoSessionManager(cfg safeagent.Config) *videoSessionManager {
	dir := filepath.Join(os.TempDir(), "safeagent-frames")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("video: cannot create frame dir %s: %v", dir, err)
	}
	return &videoSessionManager{
		sessions: make(map[string]*safeagent.VideoSession),
		cfg:      cfg,
		frameDir: dir,
	}
}

func (m *videoSessionManager) create() (id string, sess *safeagent.VideoSession) {
	id = uuid.NewString()
	sess = safeagent.NewVideoSession(m.cfg)
	m.mu.Lock()
	m.sessions[id] = sess
	m.mu.Unlock()

	// Automatically clean up sessions that are left open for too long.
	go func() {
		time.Sleep(2 * time.Hour)
		m.remove(id)
	}()
	return id, sess
}

func (m *videoSessionManager) get(id string) (*safeagent.VideoSession, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s, ok := m.sessions[id]
	return s, ok
}

func (m *videoSessionManager) remove(id string) {
	m.mu.Lock()
	if s, ok := m.sessions[id]; ok {
		s.Stop()
		delete(m.sessions, id)
	}
	m.mu.Unlock()
}

// ── Handlers ───────────────────────────────────────────────────────────────

// HandleVideoInspect returns all HTTP handlers for the video inspection API.
//
//	POST   /api/safeagent/video/start  → create session, return {"session_id":"..."}
//	GET    /api/safeagent/video/events → SSE stream (?session_id=...)
//	POST   /api/safeagent/video/frame  → submit a frame (multipart)
//	POST   /api/safeagent/video/stop   → stop a session
//	GET    /api/safeagent/video/frames/{filename} → serve temp JPEG files
func HandleVideoInspect(cfg safeagent.Config) (start, events, frame, stop, serveFrame http.Handler) {
	mgr := newVideoSessionManager(cfg)

	// POST /api/safeagent/video/start
	start = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		id, _ := mgr.create()
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(map[string]string{"session_id": id})
	})

	// GET /api/safeagent/video/events?session_id=xxx
	events = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sid := r.URL.Query().Get("session_id")
		sess, ok := mgr.get(sid)
		if !ok {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")

		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}

		for {
			select {
			case <-r.Context().Done():
				return
			case ev, open := <-sess.Events():
				if !open {
					return
				}
				b, _ := json.Marshal(ev)
				fmt.Fprintf(w, "data: %s\n\n", b)
				flusher.Flush()
				if ev.Type == safeagent.VideoEventSessionEnd {
					return
				}
			}
		}
	})

	// POST /api/safeagent/video/frame  (multipart/form-data)
	// Fields: session_id, frame_idx, timestamp, description, frame (file)
	frame = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseMultipartForm(10 << 20); err != nil {
			http.Error(w, "multipart parse error: "+err.Error(), http.StatusBadRequest)
			return
		}

		sid := r.FormValue("session_id")
		sess, ok := mgr.get(sid)
		if !ok {
			http.Error(w, "session not found", http.StatusNotFound)
			return
		}

		var idx int
		fmt.Sscanf(r.FormValue("frame_idx"), "%d", &idx)
		var ts float64
		fmt.Sscanf(r.FormValue("timestamp"), "%f", &ts)
		desc := r.FormValue("description")

		// Save uploaded JPEG to a temp file so the pipeline can reference it.
		imageURL := ""
		if f, _, err := r.FormFile("frame"); err == nil {
			defer f.Close()
			fname := fmt.Sprintf("frame-%d-%d.jpg", idx, time.Now().UnixNano())
			fpath := filepath.Join(mgr.frameDir, fname)
			if out, err := os.Create(fpath); err == nil {
				io.Copy(out, f)
				out.Close()
				imageURL = fmt.Sprintf("http://localhost:8080/api/safeagent/video/frames/%s", fname)
				go func() {
					time.Sleep(10 * time.Minute)
					os.Remove(fpath)
				}()
			} else {
				log.Printf("video: save frame %d: %v", idx, err)
			}
		}

		sess.Submit(safeagent.FrameJob{
			Idx:       idx,
			Timestamp: ts,
			ImageURL:  imageURL,
			Desc:      desc,
		})
		w.WriteHeader(http.StatusAccepted)
	})

	// POST /api/safeagent/video/stop
	stop = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var body struct {
			SessionID string `json:"session_id"`
		}
		json.NewDecoder(r.Body).Decode(&body)
		mgr.remove(body.SessionID)
		w.WriteHeader(http.StatusNoContent)
	})

	// GET /api/safeagent/video/frames/{filename}
	serveFrame = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fname := filepath.Base(r.URL.Path)
		fpath := filepath.Join(mgr.frameDir, fname)
		// Basic path traversal guard.
		if fname == "." || fname == "/" || fname == "" {
			http.NotFound(w, r)
			return
		}
		http.ServeFile(w, r, fpath)
	})

	return start, events, frame, stop, serveFrame
}
