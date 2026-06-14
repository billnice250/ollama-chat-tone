package main

import (
	"context"
	"database/sql"
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

	authm, err := auth.New(contextBackground(), cfg, store)
	if err != nil {
		log.Fatal(err)
	}
	oc := ollama.New(cfg.OllamaURL, cfg.OllamaTimeout)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	mux.HandleFunc("/auth/login", authm.Login)
	mux.HandleFunc("/auth/signup", authm.Signup)
	mux.HandleFunc("/auth/callback", authm.Callback)
	mux.HandleFunc("/auth/logout", authm.Logout)
	mux.HandleFunc("/styles.css", servePublicStatic("styles.css"))
	mux.Handle("/", authm.RequireAuth(http.FileServer(http.FS(staticFiles()))))
	mux.Handle("/api/config", authm.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		user := userFromRequest(r)
		auth.WriteJSON(w, map[string]any{
			"appName":      cfg.AppName,
			"defaultModel": cfg.DefaultModel,
			"authMode":     cfg.AuthMode(),
			"storageMode":  storageMode(cfg.AuthMode()),
			"currentUser":  user,
			"isAdmin":      isAdmin(r),
		})
	})))
	mux.Handle("/api/admin/users", authm.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isAdmin(r) {
			writeError(w, http.StatusForbidden, "admin access required")
			return
		}
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		users, err := store.ListUsers(r.Context())
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		auth.WriteJSON(w, map[string]any{"users": users})
	})))
	mux.Handle("/api/admin/users/", authm.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isAdmin(r) {
			writeError(w, http.StatusForbidden, "admin access required")
			return
		}
		path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/users/"), "/")
		username, action, ok := strings.Cut(path, "/")
		if !ok || action != "approve" || username == "" {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if err := store.ApproveUser(r.Context(), username); err != nil {
			writeDBError(w, err, "user not found")
			return
		}
		auth.WriteJSON(w, map[string]any{"approved": true})
	})))
	mux.Handle("/api/conversations", authm.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := userFromRequest(r)
		if user == "anonymous" {
			writeError(w, http.StatusForbidden, "anonymous chats are stored in local browser storage")
			return
		}

		switch r.Method {
		case http.MethodGet:
			conversations, err := store.ListConversations(r.Context(), user)
			if err != nil {
				log.Printf("list conversations error user=%q err=%v", user, err)
				writeError(w, http.StatusInternalServerError, "could not load conversations")
				return
			}
			auth.WriteJSON(w, map[string]any{"conversations": conversations})
		case http.MethodPost:
			var req struct {
				Title string `json:"title"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, "bad request")
				return
			}
			conversation, err := store.CreateConversation(r.Context(), user, req.Title)
			if err != nil {
				log.Printf("create conversation error user=%q err=%v", user, err)
				writeError(w, http.StatusInternalServerError, "could not create conversation")
				return
			}
			auth.WriteJSON(w, map[string]any{"conversation": conversation})
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})))
	mux.Handle("/api/conversations/", authm.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := userFromRequest(r)
		if user == "anonymous" {
			writeError(w, http.StatusForbidden, "anonymous chats are stored in local browser storage")
			return
		}
		handleConversation(w, r, store, user)
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

	listener, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		log.Fatal(err)
	}
	actualAddr := listener.Addr().String()
	log.Printf("%s running at %s", cfg.AppName, appURL(actualAddr))
	log.Printf("listening on %s configured=%s auth=%s ollama=%s timeout=%s", actualAddr, cfg.Addr, cfg.AuthMode(), cfg.OllamaURL, cfg.OllamaTimeout)
	log.Fatal(http.Serve(listener, requestLogger(mux)))
}

func contextBackground() context.Context { return context.Background() }

func servePublicStatic(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		http.ServeFileFS(w, r, staticFiles(), name)
	}
}

func storageMode(authMode string) string {
	if authMode == "none" {
		return "local"
	}
	return "server"
}

func userFromRequest(r *http.Request) string {
	if user, ok := r.Context().Value(auth.EmailKey).(string); ok && user != "" {
		return user
	}
	return "anonymous"
}

func isAdmin(r *http.Request) bool {
	v, _ := r.Context().Value(auth.AdminKey).(bool)
	return v
}

func handleConversation(w http.ResponseWriter, r *http.Request, store *db.Store, user string) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/conversations/"), "/")
	parts := strings.Split(path, "/")
	if len(parts) == 0 || parts[0] == "" {
		writeError(w, http.StatusNotFound, "conversation not found")
		return
	}
	conversationID := parts[0]

	if len(parts) == 1 {
		switch r.Method {
		case http.MethodGet:
			conversation, err := store.GetConversation(r.Context(), user, conversationID)
			if err != nil {
				writeDBError(w, err, "conversation not found")
				return
			}
			auth.WriteJSON(w, map[string]any{"conversation": conversation})
		case http.MethodPatch:
			var req struct {
				Title string `json:"title"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, "bad request")
				return
			}
			if err := store.UpdateConversationTitle(r.Context(), user, conversationID, req.Title); err != nil {
				writeDBError(w, err, "conversation not found")
				return
			}
			conversation, err := store.GetConversation(r.Context(), user, conversationID)
			if err != nil {
				writeDBError(w, err, "conversation not found")
				return
			}
			auth.WriteJSON(w, map[string]any{"conversation": conversation})
		case http.MethodDelete:
			if err := store.DeleteConversation(r.Context(), user, conversationID); err != nil {
				writeDBError(w, err, "conversation not found")
				return
			}
			auth.WriteJSON(w, map[string]any{"deleted": true})
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
		return
	}

	if len(parts) == 2 && parts[1] == "messages" {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req struct {
			Role     string `json:"role"`
			Content  string `json:"content"`
			Thinking string `json:"thinking"`
			Model    string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad request")
			return
		}
		msg, err := store.AddMessage(r.Context(), user, conversationID, db.Message{
			Role:     req.Role,
			Content:  req.Content,
			Thinking: req.Thinking,
			Model:    req.Model,
		})
		if err != nil {
			writeDBError(w, err, "conversation not found")
			return
		}
		auth.WriteJSON(w, map[string]any{"message": msg})
		return
	}

	writeError(w, http.StatusNotFound, "conversation not found")
}

func writeDBError(w http.ResponseWriter, err error, notFound string) {
	if errors.Is(err, sql.ErrNoRows) {
		writeError(w, http.StatusNotFound, notFound)
		return
	}
	writeError(w, http.StatusInternalServerError, err.Error())
}

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

func appURL(addr string) string {
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
