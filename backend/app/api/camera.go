package api

import (
	"log"
	"net/http"
	"os/exec"
)

// HandleCameraStream proxies an RTSP stream to the browser as MJPEG
// (multipart/x-mixed-replace) using an FFmpeg subprocess.
//
// Query params:
//
//	url  — the RTSP source URL, e.g. rtsp://user:pass@192.168.1.10:554/stream
//	fps  — output frame rate (default "15")
//	q    — JPEG quality 1-31, lower=better (default "5")
func HandleCameraStream() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rtspURL := r.URL.Query().Get("url")
		if rtspURL == "" {
			http.Error(w, "missing query param: url", http.StatusBadRequest)
			return
		}

		fps := r.URL.Query().Get("fps")
		if fps == "" {
			fps = "15"
		}
		q := r.URL.Query().Get("q")
		if q == "" {
			q = "5"
		}

		// Verify ffmpeg is available.
		ffmpegPath, err := exec.LookPath("ffmpeg")
		if err != nil {
			http.Error(w, "ffmpeg not found — please install ffmpeg and ensure it is in PATH", http.StatusNotImplemented)
			return
		}

		// FFmpeg mpjpeg muxer outputs multipart/x-mixed-replace frames directly.
		cmd := exec.CommandContext(r.Context(), ffmpegPath,
			"-rtsp_transport", "tcp",
			"-i", rtspURL,
			"-f", "mpjpeg",
			"-q:v", q,
			"-r", fps,
			"-",
		)

		cmd.Stdout = w

		// Capture stderr for logging only.
		cmd.Stderr = nil

		// Set streaming headers before the command starts writing.
		w.Header().Set("Content-Type", "multipart/x-mixed-replace; boundary=ffmpeg")
		w.Header().Set("Cache-Control", "no-cache, no-store")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Connection", "close")

		// Flush headers.
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}

		if err := cmd.Run(); err != nil {
			// Client disconnected or ffmpeg exited — expected, no need to log as error.
			if r.Context().Err() == nil {
				log.Printf("camera stream ended: %v (url=%s)", err, rtspURL)
			}
		}
	})
}
