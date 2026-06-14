package main

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/billnice250/ollama-chat-client/internal/auth"
	"github.com/billnice250/ollama-chat-client/internal/config"
	"github.com/billnice250/ollama-chat-client/internal/db"
	"github.com/billnice250/ollama-chat-client/internal/ollama"
)

func main() {
	cfg := config.Load()
	store, err := db.Open(cfg.DBPath)
	if err != nil {
		log.Fatal(err)
	}
	defer store.DB.Close()

	authm, err := auth.New(contextBackground(), cfg)
	if err != nil {
		log.Fatal(err)
	}
	oc := ollama.New(cfg.OllamaURL)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	mux.HandleFunc("/auth/login", authm.Login)
	mux.HandleFunc("/auth/callback", authm.Callback)
	mux.HandleFunc("/auth/logout", authm.Logout)
	mux.Handle("/", authm.RequireAuth(http.FileServer(http.Dir(webDir()))))
	mux.Handle("/api/config", authm.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		auth.WriteJSON(w, map[string]any{
			"defaultModel": cfg.DefaultModel,
			"authMode":     cfg.AuthMode(),
		})
	})))
	mux.Handle("/api/models", authm.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		models, err := oc.Models(r.Context())
		if err != nil {
			log.Printf("models error remote=%s err=%v", r.RemoteAddr, err)
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		auth.WriteJSON(w, map[string]any{"models": models})
	})))
	mux.Handle("/api/chat", authm.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req ollama.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			log.Printf("chat decode error remote=%s err=%v", r.RemoteAddr, err)
			writeError(w, http.StatusBadRequest, "bad request")
			return
		}
		if req.Model == "" {
			req.Model = cfg.DefaultModel
		}
		res, err := oc.Chat(r.Context(), req)
		if err != nil {
			log.Printf("chat error remote=%s model=%q err=%v", r.RemoteAddr, req.Model, err)
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		log.Printf("chat ok remote=%s model=%q messages=%d", r.RemoteAddr, req.Model, len(req.Messages))
		auth.WriteJSON(w, res)
	})))

	log.Printf("app running at %s", appURL(cfg.BaseURL, cfg.Addr))
	log.Printf("listening on %s auth=%s ollama=%s", cfg.Addr, cfg.AuthMode(), cfg.OllamaURL)
	log.Fatal(http.ListenAndServe(cfg.Addr, requestLogger(mux)))
}

func contextBackground() context.Context { return context.Background() }

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lw := &loggingResponseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(lw, r)
		log.Printf("%s %s status=%d remote=%s", r.Method, r.URL.Path, lw.status, r.RemoteAddr)
	})
}

type loggingResponseWriter struct {
	http.ResponseWriter
	status int
}

func (w *loggingResponseWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func appURL(baseURL, addr string) string {
	if baseURL != "" {
		return strings.TrimRight(baseURL, "/")
	}

	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "http://" + addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "localhost"
	}
	return "http://" + net.JoinHostPort(host, port)
}

func webDir() string {
	if dir := os.Getenv("WEB_DIR"); dir != "" {
		return dir
	}

	var roots []string
	if wd, err := os.Getwd(); err == nil {
		roots = append(roots, wd)
	}
	if exe, err := os.Executable(); err == nil {
		roots = append(roots, filepath.Dir(exe))
	}

	for _, root := range roots {
		for dir := root; ; dir = filepath.Dir(dir) {
			candidate := filepath.Join(dir, "web")
			if st, err := os.Stat(filepath.Join(candidate, "index.html")); err == nil && !st.IsDir() {
				return candidate
			}

			parent := filepath.Dir(dir)
			if parent == dir {
				break
			}
		}
	}

	return "web"
}
