// Command safeagent-server starts an HTTP server exposing the SafeAgent API.
//
// Usage:
//
//	go run ./cmd/safeagent-server
//
// Reads backend/.env for LLM credentials, listens on :8080.
package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"runtime"

	"github.com/ubuildingagent/backend/agents/safeagent"
	"github.com/ubuildingagent/backend/app/api"
	"github.com/ubuildingagent/backend/internal/envconfig"
	_ "github.com/ubuildingagent/backend/llmprovider/providers" // register all providers
)

func main() {
	addr := flag.String("addr", ":8080", "HTTP listen address")
	envFile := flag.String("env", defaultEnvFile(), "path to .env file")
	staticDir := flag.String("static", "", "directory to serve as static files at / (optional)")
	flag.Parse()

	cfg, err := envconfig.LoadFromFile(*envFile)
	if err != nil {
		log.Fatalf("failed to load .env: %v", err)
	}
	if err := cfg.Validate(); err != nil {
		log.Fatalf("incomplete .env config: %v", err)
	}
	log.Printf("LLM: type=%s model=%s baseURL=%s", cfg.Type, cfg.Model, cfg.BaseURL)

	vlmCfg, err := envconfig.LoadVLMFromFile(*envFile)
	if err != nil {
		log.Fatalf("failed to load VLM config from .env: %v", err)
	}
	if vlmCfg.IsConfigured() {
		log.Printf("VLM: type=%s model=%s baseURL=%s", vlmCfg.Type, vlmCfg.Model, vlmCfg.BaseURL)
	} else {
		log.Printf("VLM: not configured — vision tools will run text-only")
	}

	agentCfg := safeagent.Config{
		APIKey:              cfg.APIKey,
		Model:               cfg.ToModel(),
		VLMModel:            vlmCfg.ToModel(),
		VLMAPIKey:           vlmCfg.APIKey,
		OrchestratorMaxIter: 20,
		SubAgentMaxIter:     10,
	}

	mux := http.NewServeMux()

	// SafeAgent SSE endpoint.
	mux.Handle("/api/safeagent/inspect", api.HandleInspect(agentCfg))

	// RTSP → MJPEG camera proxy endpoint.
	mux.Handle("/api/camera/stream", api.HandleCameraStream())

	// Video frame inspection endpoints (sliding-window multi-agent pipeline).
	videoStart, videoEvents, videoFrame, videoStop, videoServeFrame :=
		api.HandleVideoInspect(agentCfg)
	mux.Handle("/api/safeagent/video/start", videoStart)
	mux.Handle("/api/safeagent/video/events", videoEvents)
	mux.Handle("/api/safeagent/video/frame", videoFrame)
	mux.Handle("/api/safeagent/video/stop", videoStop)
	mux.Handle("/api/safeagent/video/frames/", videoServeFrame)

	// Optional: serve frontend static files.
	if *staticDir != "" {
		fs := http.FileServer(http.Dir(*staticDir))
		mux.Handle("/", fs)
		log.Printf("Serving static files from: %s", *staticDir)
	}

	handler := corsMiddleware(mux)

	fmt.Printf("\nSafeAgent server listening on http://localhost%s\n\n", *addr)
	if err := http.ListenAndServe(*addr, handler); err != nil {
		log.Fatalf("server error: %v", err)
	}
}

// corsMiddleware adds permissive CORS headers for local development.
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// defaultEnvFile returns the path to backend/.env relative to this source file.
// Falls back to a path relative to the working directory.
func defaultEnvFile() string {
	_, thisFile, _, ok := runtime.Caller(0)
	if ok {
		// cmd/safeagent-server/main.go → ../../.env
		return filepath.Join(filepath.Dir(thisFile), "..", "..", ".env")
	}
	// Fallback: look for .env in cwd or parent.
	if _, err := os.Stat(".env"); err == nil {
		return ".env"
	}
	return filepath.Join("..", ".env")
}
