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
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"sync"
	"syscall"

	"github.com/billnice250/ollama-chat-tone/internal/auth"
	"github.com/billnice250/ollama-chat-tone/internal/config"
	"github.com/billnice250/ollama-chat-tone/internal/db"
	"github.com/billnice250/ollama-chat-tone/internal/ollama"
	"github.com/billnice250/ollama-chat-tone/internal/static"
)

func main() {
	cfg := config.Load()
	store, err := db.Open(cfg.DBPath)
	if err != nil {
		log.Fatal(err)
	}
	defer store.DB.Close()

	app, err := newAppRuntime(contextBackground(), cfg, store)
	if err != nil {
		log.Fatal(err)
	}
	jobs := newJobManager()
	watchReloadSignal(app)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	mux.HandleFunc("/auth/login", app.Login)
	mux.HandleFunc("/auth/signup", app.Signup)
	mux.HandleFunc("/auth/callback", app.Callback)
	mux.HandleFunc("/auth/logout", app.Logout)
	mux.HandleFunc("/styles.css", servePublicStatic("styles.css"))
	mux.HandleFunc("/logo.svg", servePublicStatic("logo.svg"))
	mux.Handle("/", app.RequireAuth(http.FileServer(http.FS(staticFiles()))))
	mux.Handle("/api/account", app.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodDelete {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		user := userFromRequest(r)
		if user == "anonymous" {
			writeError(w, http.StatusForbidden, "anonymous account cannot be deleted")
			return
		}
		if err := store.DeleteAccount(r.Context(), user); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		clearAppCookie(w, "email")
		clearAppCookie(w, "session")
		clearAppCookie(w, "state")
		auth.WriteJSON(w, map[string]any{"deleted": true})
	})))
	mux.Handle("/api/config", app.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		user := userFromRequest(r)
		cfg := app.Config()
		auth.WriteJSON(w, map[string]any{
			"appName":      cfg.AppName,
			"defaultModel": cfg.DefaultModel,
			"authMode":     cfg.AuthMode(),
			"storageMode":  storageMode(cfg.AuthMode()),
			"currentUser":  user,
			"isAdmin":      isAdmin(r),
		})
	})))
	mux.Handle("/api/config/reload", app.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if userFromRequest(r) != "anonymous" && !isAdmin(r) {
			writeError(w, http.StatusForbidden, "admin access required")
			return
		}
		cfg, warnings, err := app.Reload(r.Context())
		if err != nil {
			log.Printf("config reload error remote=%s err=%v", r.RemoteAddr, err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		log.Printf("config reloaded remote=%s app=%q auth=%s ollama=%s timeout=%s warnings=%s", r.RemoteAddr, cfg.AppName, cfg.AuthMode(), cfg.OllamaURL, cfg.OllamaTimeout, strings.Join(warnings, "; "))
		auth.WriteJSON(w, map[string]any{
			"reloaded":     true,
			"appName":      cfg.AppName,
			"defaultModel": cfg.DefaultModel,
			"authMode":     cfg.AuthMode(),
			"storageMode":  storageMode(cfg.AuthMode()),
			"warnings":     warnings,
		})
	})))
	mux.Handle("/api/admin/users", app.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	mux.Handle("/api/admin/users/", app.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isAdmin(r) {
			writeError(w, http.StatusForbidden, "admin access required")
			return
		}
		path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/users/"), "/")
		username, action, ok := strings.Cut(path, "/")
		if !ok || username == "" || (action != "approve" && action != "revoke") {
			writeError(w, http.StatusNotFound, "user not found")
			return
		}
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var err error
		if action == "approve" {
			err = store.ApproveUser(r.Context(), username)
		} else {
			if username == userFromRequest(r) {
				writeError(w, http.StatusBadRequest, "cannot revoke your own account")
				return
			}
			err = store.RevokeUser(r.Context(), username)
		}
		if err != nil {
			writeDBError(w, err, "user not found")
			return
		}
		auth.WriteJSON(w, map[string]any{"approved": action == "approve"})
	})))
	mux.Handle("/api/conversations", app.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
	mux.Handle("/api/conversations/", app.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := userFromRequest(r)
		if user == "anonymous" {
			writeError(w, http.StatusForbidden, "anonymous chats are stored in local browser storage")
			return
		}
		handleConversation(w, r, store, app.Ollama(), jobs, user)
	})))
	mux.Handle("/api/models", app.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		models, err := app.Ollama().Models(r.Context())
		if err != nil {
			log.Printf("models error remote=%s err=%v", r.RemoteAddr, err)
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		auth.WriteJSON(w, map[string]any{"models": models})
	})))
	mux.Handle("/api/chat", app.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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
			req.Model = app.Config().DefaultModel
		}
		if req.Stream {
			if err := streamChat(w, r, app.Ollama(), req); err != nil {
				if errors.Is(err, context.Canceled) {
					log.Printf("chat stream canceled remote=%s model=%q", r.RemoteAddr, req.Model)
				} else {
					log.Printf("chat stream error remote=%s model=%q err=%v", r.RemoteAddr, req.Model, err)
				}
			}
			return
		}
		res, err := app.Ollama().Chat(r.Context(), req)
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
		if addressInUse(err) {
			url := appURL(cfg.Addr)
			log.Printf("%s appears to already be running at %s", cfg.AppName, url)
			if err := openBrowser(url); err != nil {
				log.Printf("open browser error url=%s err=%v", url, err)
			}
			return
		}
		log.Fatal(err)
	}
	actualAddr := listener.Addr().String()
	url := appURL(actualAddr)
	log.Printf("%s running at %s", cfg.AppName, url)
	log.Printf("listening on %s configured=%s auth=%s ollama=%s timeout=%s", actualAddr, cfg.Addr, cfg.AuthMode(), cfg.OllamaURL, cfg.OllamaTimeout)
	if cfg.OpenBrowser {
		go func() {
			if err := openBrowser(url); err != nil {
				log.Printf("open browser error url=%s err=%v", url, err)
			}
		}()
	}
	log.Fatal(http.Serve(listener, requestLogger(mux)))
}

func contextBackground() context.Context { return context.Background() }

func watchReloadSignal(app *appRuntime) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGHUP)
	go func() {
		for range signals {
			cfg, warnings, err := app.Reload(context.Background())
			if err != nil {
				log.Printf("config reload signal error err=%v", err)
				continue
			}
			log.Printf("config reloaded signal app=%q auth=%s ollama=%s timeout=%s warnings=%s", cfg.AppName, cfg.AuthMode(), cfg.OllamaURL, cfg.OllamaTimeout, strings.Join(warnings, "; "))
		}
	}()
}

type appRuntime struct {
	mu    sync.RWMutex
	cfg   config.Config
	authm *auth.Manager
	oc    *ollama.Client
	store *db.Store
}

func newAppRuntime(ctx context.Context, cfg config.Config, store *db.Store) (*appRuntime, error) {
	authm, err := auth.New(ctx, cfg, store)
	if err != nil {
		return nil, err
	}
	return &appRuntime{
		cfg:   cfg,
		authm: authm,
		oc:    ollama.New(cfg.OllamaURL, cfg.OllamaTimeout),
		store: store,
	}, nil
}

func (a *appRuntime) Config() config.Config {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.cfg
}

func (a *appRuntime) Ollama() *ollama.Client {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.oc
}

func (a *appRuntime) Auth() *auth.Manager {
	a.mu.RLock()
	defer a.mu.RUnlock()
	return a.authm
}

func (a *appRuntime) Reload(ctx context.Context) (config.Config, []string, error) {
	next := config.Load()

	a.mu.RLock()
	current := a.cfg
	a.mu.RUnlock()

	var warnings []string
	if next.Addr != current.Addr {
		warnings = append(warnings, "ADDR changes require restart")
		next.Addr = current.Addr
	}
	if next.DBPath != current.DBPath {
		warnings = append(warnings, "DB_PATH changes require restart")
		next.DBPath = current.DBPath
	}

	authm, err := auth.New(ctx, next, a.store)
	if err != nil {
		return current, warnings, err
	}
	oc := ollama.New(next.OllamaURL, next.OllamaTimeout)

	a.mu.Lock()
	a.cfg = next
	a.authm = authm
	a.oc = oc
	a.mu.Unlock()

	return next, warnings, nil
}

func (a *appRuntime) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a.Auth().RequireAuth(next).ServeHTTP(w, r)
	})
}

func (a *appRuntime) Login(w http.ResponseWriter, r *http.Request) {
	a.Auth().Login(w, r)
}

func (a *appRuntime) Signup(w http.ResponseWriter, r *http.Request) {
	a.Auth().Signup(w, r)
}

func (a *appRuntime) Callback(w http.ResponseWriter, r *http.Request) {
	a.Auth().Callback(w, r)
}

func (a *appRuntime) Logout(w http.ResponseWriter, r *http.Request) {
	a.Auth().Logout(w, r)
}

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

type jobManager struct {
	mu      sync.Mutex
	cancels map[string]context.CancelFunc
}

func newJobManager() *jobManager {
	return &jobManager{cancels: map[string]context.CancelFunc{}}
}

func (m *jobManager) add(id string, cancel context.CancelFunc) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cancels[id] = cancel
}

func (m *jobManager) remove(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.cancels, id)
}

func (m *jobManager) cancel(id string) {
	m.mu.Lock()
	cancel := m.cancels[id]
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func handleConversation(w http.ResponseWriter, r *http.Request, store *db.Store, oc *ollama.Client, jobs *jobManager, user string) {
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

	if len(parts) >= 2 && parts[1] == "jobs" {
		handleChatJob(w, r, store, oc, jobs, user, conversationID, parts[2:])
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

func handleChatJob(w http.ResponseWriter, r *http.Request, store *db.Store, oc *ollama.Client, jobs *jobManager, user, conversationID string, parts []string) {
	if len(parts) == 0 {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req struct {
			Model    string           `json:"model"`
			Messages []ollama.Message `json:"messages"`
			Think    bool             `json:"think"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "bad request")
			return
		}
		if req.Model == "" {
			req.Model = "unknown"
		}
		if _, err := store.ActiveChatJob(r.Context(), user, conversationID); err == nil {
			writeError(w, http.StatusConflict, "conversation already has a running response")
			return
		} else if !errors.Is(err, sql.ErrNoRows) {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		job, err := store.CreateChatJob(r.Context(), user, conversationID, req.Model)
		if err != nil {
			writeDBError(w, err, "conversation not found")
			return
		}
		ctx, cancel := context.WithCancel(context.Background())
		jobs.add(job.ID, cancel)
		go runChatJob(ctx, store, oc, jobs, user, conversationID, job.ID, req.Model, req.Messages, req.Think)
		auth.WriteJSON(w, map[string]any{"job": job})
		return
	}

	if len(parts) == 1 && parts[0] == "active" {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		job, err := store.ActiveChatJob(r.Context(), user, conversationID)
		if errors.Is(err, sql.ErrNoRows) {
			auth.WriteJSON(w, map[string]any{"job": nil})
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		auth.WriteJSON(w, map[string]any{"job": job})
		return
	}

	if len(parts) >= 1 {
		jobID := parts[0]
		if len(parts) == 1 && r.Method == http.MethodGet {
			job, err := store.GetChatJob(r.Context(), user, conversationID, jobID)
			if err != nil {
				writeDBError(w, err, "job not found")
				return
			}
			auth.WriteJSON(w, map[string]any{"job": job})
			return
		}
		if len(parts) == 2 && parts[1] == "cancel" && r.Method == http.MethodPost {
			jobs.cancel(jobID)
			if err := store.CancelChatJob(r.Context(), user, conversationID, jobID); err != nil {
				writeDBError(w, err, "job not found")
				return
			}
			auth.WriteJSON(w, map[string]any{"canceled": true})
			return
		}
	}

	writeError(w, http.StatusNotFound, "job not found")
}

func runChatJob(ctx context.Context, store *db.Store, oc *ollama.Client, jobs *jobManager, user, conversationID, jobID, model string, messages []ollama.Message, think bool) {
	defer jobs.remove(jobID)
	content := ""
	thinking := ""
	req := ollama.ChatRequest{Model: model, Messages: messages, Stream: true, Think: &think}
	err := oc.StreamChat(ctx, req, func(chunk ollama.ChatResponse) error {
		content += chunk.Message.Content
		if think {
			thinking += chunk.Message.Thinking
		}
		return store.UpdateChatJob(context.Background(), user, conversationID, jobID, content, thinking)
	})
	if err != nil {
		if errors.Is(err, context.Canceled) {
			_ = store.CancelChatJob(context.Background(), user, conversationID, jobID)
			return
		}
		_ = store.FailChatJob(context.Background(), user, conversationID, jobID, err.Error())
		return
	}
	if _, err := store.CompleteChatJob(context.Background(), user, conversationID, jobID, content, thinking, model); err != nil {
		_ = store.FailChatJob(context.Background(), user, conversationID, jobID, err.Error())
	}
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

func clearAppCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
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

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}

func addressInUse(err error) bool {
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "address already in use") ||
		strings.Contains(msg, "only one usage of each socket address")
}

func staticFiles() fs.FS {
	sub, err := static.Files()
	if err != nil {
		log.Fatal(err)
	}
	return sub
}
