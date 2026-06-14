package main

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net"
	"net/http"
	"strings"

	"github.com/billnice250/ollama-chat-client/internal/auth"
	"github.com/billnice250/ollama-chat-client/internal/config"
	"github.com/billnice250/ollama-chat-client/internal/db"
	"github.com/billnice250/ollama-chat-client/internal/ollama"
	"github.com/billnice250/ollama-chat-client/internal/static"
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
	mux.Handle("/", authm.RequireAuth(http.FileServer(http.FS(staticFiles()))))
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
		if req.Stream {
			if err := streamChat(w, r, oc, req); err != nil {
				if errors.Is(err, context.Canceled) {
					log.Printf("chat stream canceled remote=%s model=%q", r.RemoteAddr, req.Model)
				} else {
					log.Printf("chat stream error remote=%s model=%q err=%v", r.RemoteAddr, req.Model, err)
				}
			}
			return
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

func (w *loggingResponseWriter) Flush() {
	if flusher, ok := w.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func writeError(w http.ResponseWriter, status int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func streamChat(w http.ResponseWriter, r *http.Request, oc *ollama.Client, req ollama.ChatRequest) error {
	rc := http.NewResponseController(w)

	w.Header().Set("Content-Type", "application/x-ndjson")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	enc := json.NewEncoder(w)
	var chunks int
	var thinkingChunks int
	err := oc.StreamChat(r.Context(), req, func(chunk ollama.ChatResponse) error {
		chunks++
		if chunk.Message.Thinking != "" {
			thinkingChunks++
			if thinkingChunks == 1 {
				log.Printf("chat stream thinking remote=%s model=%q", r.RemoteAddr, req.Model)
			}
		}
		if err := enc.Encode(chunk); err != nil {
			return err
		}
		if err := rc.Flush(); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			return err
		}
		_ = enc.Encode(map[string]string{"error": err.Error()})
		_ = rc.Flush()
		return err
	}

	log.Printf("chat stream ok remote=%s model=%q messages=%d chunks=%d thinking_chunks=%d", r.RemoteAddr, req.Model, len(req.Messages), chunks, thinkingChunks)
	return nil
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

func staticFiles() fs.FS {
	sub, err := static.Files()
	if err != nil {
		log.Fatal(err)
	}
	return sub
}
