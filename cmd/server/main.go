package main

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/billnice250/ollama-chat-tone/internal/auth"
	"github.com/billnice250/ollama-chat-tone/internal/config"
	"github.com/billnice250/ollama-chat-tone/internal/db"
	"github.com/billnice250/ollama-chat-tone/internal/logger"
	"github.com/billnice250/ollama-chat-tone/internal/ollama"
	"github.com/billnice250/ollama-chat-tone/internal/static"
	"golang.org/x/crypto/pkcs12"
)

var buildTime = ""
var appLog = logger.NewFromEnv().With("component", "server")

func main() {
	cfg := config.Load()
	appLog = logger.New(cfg.LogLevel).With("component", "server")
	appLog.Info("configuration loaded", "authMode", cfg.AuthMode(), "addr", cfg.Addr, "dbPath", cfg.DBPath, "logLevel", cfg.LogLevel)
	store, err := db.Open(cfg.DBPath)
	if err != nil {
		appLog.Error("database open failed", "path", cfg.DBPath, "error", err)
		os.Exit(1)
	}
	appLog.Info("database opened", "path", cfg.DBPath)
	defer store.DB.Close()

	app, err := newAppRuntime(contextBackground(), cfg, store)
	if err != nil {
		appLog.Error("failed to initialize app runtime", "error", err)
		os.Exit(1)
	}
	jobs := newJobManager()
	watchReloadSignal(app)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(204) })
	mux.HandleFunc("/auth/login", app.Login)
	mux.HandleFunc("/auth/signup", app.Signup)
	mux.HandleFunc("/auth/callback", app.Callback)
	mux.HandleFunc("/auth/logout", app.Logout)
	mux.HandleFunc("/auth/verify", app.Verify)
	mux.HandleFunc("/auth/forgot-password", app.ForgotPassword)
	mux.HandleFunc("/auth/reset-password", app.ResetPassword)
	mux.HandleFunc("/styles.css", servePublicStatic("styles.css"))
	mux.HandleFunc("/logo.svg", servePublicStatic("logo.svg"))
	mux.HandleFunc("/manifest.webmanifest", serveManifest(app))
	mux.HandleFunc("/service-worker.js", servePublicStatic("service-worker.js"))
	mux.HandleFunc("/pwa.js", servePublicStatic("pwa.js"))
	mux.Handle("/", app.RequireAuth(staticFileServer(staticFiles())))
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
			"version":      appVersion(),
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
			appLog.Error("config reload error", "remote", anonymizeRemoteAddr(r.RemoteAddr), "error", err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		appLog.Info("config reloaded", "remote", anonymizeRemoteAddr(r.RemoteAddr), "app", cfg.AppName, "auth", cfg.AuthMode(), "ollama", cfg.OllamaURL, "timeout", cfg.OllamaTimeout, "warnings", strings.Join(warnings, "; "))
		auth.WriteJSON(w, map[string]any{
			"reloaded":     true,
			"appName":      cfg.AppName,
			"version":      appVersion(),
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
			appLog.Error("list admin users failed", "remote", anonymizeRemoteAddr(r.RemoteAddr), "error", err)
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		appLog.Debug("listed admin users", "remote", anonymizeRemoteAddr(r.RemoteAddr), "count", len(users))
		auth.WriteJSON(w, map[string]any{"users": users})
	})))
	mux.Handle("/api/admin/users/", app.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isAdmin(r) {
			writeError(w, http.StatusForbidden, "admin access required")
			return
		}
		path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/admin/users/"), "/")
		username, action, ok := strings.Cut(path, "/")
		if !ok || username == "" {
			writeError(w, http.StatusNotFound, "not found")
			return
		}
		switch action {
		case "approve", "revoke":
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
		case "delete-data":
			// Delete all conversations/messages of a user but keep the account.
			if r.Method != http.MethodPost {
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			if err := store.ClearUserData(r.Context(), username); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			auth.WriteJSON(w, map[string]any{"cleared": true})
		case "delete":
			// Permanently delete a user account and all their data.
			if r.Method != http.MethodPost {
				writeError(w, http.StatusMethodNotAllowed, "method not allowed")
				return
			}
			if username == userFromRequest(r) {
				writeError(w, http.StatusBadRequest, "cannot delete your own account")
				return
			}
			if err := store.DeleteAccount(r.Context(), username); err != nil {
				writeDBError(w, err, "user not found")
				return
			}
			auth.WriteJSON(w, map[string]any{"deleted": true})
		default:
			writeError(w, http.StatusNotFound, "not found")
		}
	})))
	mux.Handle("/api/admin/settings", app.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isAdmin(r) {
			writeError(w, http.StatusForbidden, "admin access required")
			return
		}
		switch r.Method {
		case http.MethodGet:
			settings, err := store.ListSettings(r.Context())
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			cfg := app.Config()
			// Merge env-based defaults with DB overrides for display.
			effective := map[string]any{
				"ollamaURL":     firstNonEmpty(settings["ollama_url"], cfg.OllamaURL),
				"ollamaTimeout": firstNonEmpty(settings["ollama_timeout"], cfg.OllamaTimeout.String()),
				"defaultModel":  firstNonEmpty(settings["ollama_default_model"], cfg.DefaultModel),
				"mtlsEnabled":   settings["ollama_tls_pfx"] != "",
			}
			auth.WriteJSON(w, map[string]any{"settings": effective})
		case http.MethodPost:
			var req struct {
				OllamaURL     string `json:"ollamaURL"`
				OllamaTimeout string `json:"ollamaTimeout"`
				DefaultModel  string `json:"defaultModel"`
			}
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				writeError(w, http.StatusBadRequest, "bad request")
				return
			}
			if req.OllamaURL != "" {
				if err := store.SetSetting(r.Context(), "ollama_url", req.OllamaURL); err != nil {
					writeError(w, http.StatusInternalServerError, err.Error())
					return
				}
			}
			if req.OllamaTimeout != "" {
				if _, err := time.ParseDuration(req.OllamaTimeout); err != nil {
					if _, err2 := strconv.Atoi(req.OllamaTimeout); err2 != nil {
						writeError(w, http.StatusBadRequest, "ollamaTimeout must be a duration (e.g. 5m) or minutes integer")
						return
					}
				}
				if err := store.SetSetting(r.Context(), "ollama_timeout", req.OllamaTimeout); err != nil {
					writeError(w, http.StatusInternalServerError, err.Error())
					return
				}
			}
			if req.DefaultModel != "" {
				if err := store.SetSetting(r.Context(), "ollama_default_model", req.DefaultModel); err != nil {
					writeError(w, http.StatusInternalServerError, err.Error())
					return
				}
			}
			// Apply settings immediately.
			cfg, warnings, err := app.Reload(r.Context())
			if err != nil {
				appLog.Error("settings apply error", "remote", anonymizeRemoteAddr(r.RemoteAddr), "error", err)
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			appLog.Info("settings updated", "remote", anonymizeRemoteAddr(r.RemoteAddr), "app", cfg.AppName, "ollama", cfg.OllamaURL, "timeout", cfg.OllamaTimeout, "warnings", strings.Join(warnings, "; "))
			auth.WriteJSON(w, map[string]any{"applied": true, "warnings": warnings})
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	})))
	mux.Handle("/api/admin/settings/ollama-cert", app.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !isAdmin(r) {
			writeError(w, http.StatusForbidden, "admin access required")
			return
		}
		switch r.Method {
		case http.MethodPost:
			if err := r.ParseMultipartForm(4 << 20); err != nil {
				writeError(w, http.StatusBadRequest, "bad request: "+err.Error())
				return
			}
			file, _, err := r.FormFile("cert")
			if err != nil {
				writeError(w, http.StatusBadRequest, "cert file required")
				return
			}
			defer file.Close()
			pfxBytes, err := io.ReadAll(io.LimitReader(file, 4<<20))
			if err != nil {
				writeError(w, http.StatusInternalServerError, "could not read cert file: "+err.Error())
				return
			}
			passphrase := r.FormValue("passphrase")
			// Validate the PFX before storing.
			if _, _, err := parsePFX(pfxBytes, passphrase); err != nil {
				writeError(w, http.StatusBadRequest, "invalid PFX certificate: "+err.Error())
				return
			}
			encoded := base64.StdEncoding.EncodeToString(pfxBytes)
			if err := store.SetSetting(r.Context(), "ollama_tls_pfx", encoded); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			if err := store.SetSetting(r.Context(), "ollama_tls_pfx_pass", passphrase); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			cfg, warnings, err := app.Reload(r.Context())
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			appLog.Info("mTLS cert updated", "remote", anonymizeRemoteAddr(r.RemoteAddr), "app", cfg.AppName, "warnings", strings.Join(warnings, "; "))
			auth.WriteJSON(w, map[string]any{"applied": true, "warnings": warnings})
		case http.MethodDelete:
			_ = store.DeleteSetting(r.Context(), "ollama_tls_pfx")
			_ = store.DeleteSetting(r.Context(), "ollama_tls_pfx_pass")
			cfg, warnings, err := app.Reload(r.Context())
			if err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			appLog.Info("mTLS cert removed", "remote", anonymizeRemoteAddr(r.RemoteAddr), "app", cfg.AppName, "warnings", strings.Join(warnings, "; "))
			auth.WriteJSON(w, map[string]any{"removed": true, "warnings": warnings})
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
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
				appLog.Error("list conversations error", "user", user, "error", err)
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
				appLog.Error("create conversation error", "user", user, "error", err)
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
			appLog.Error("model listing error", "remote", anonymizeRemoteAddr(r.RemoteAddr), "error", err)
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
			appLog.Warn("chat decode error", "remote", anonymizeRemoteAddr(r.RemoteAddr), "error", err)
			writeError(w, http.StatusBadRequest, "bad request")
			return
		}
		if req.Model == "" {
			req.Model = app.Config().DefaultModel
		}
		if req.Stream {
			if err := streamChat(w, r, app.Ollama(), req); err != nil {
				if errors.Is(err, context.Canceled) {
					appLog.Info("chat stream canceled", "remote", anonymizeRemoteAddr(r.RemoteAddr), "model", req.Model)
				} else {
					appLog.Error("chat stream error", "remote", anonymizeRemoteAddr(r.RemoteAddr), "model", req.Model, "error", err)
				}
			}
			return
		}
		res, err := app.Ollama().Chat(r.Context(), req)
		if err != nil {
			appLog.Error("chat error", "remote", anonymizeRemoteAddr(r.RemoteAddr), "model", req.Model, "error", err)
			writeError(w, http.StatusBadGateway, err.Error())
			return
		}
		appLog.Info("chat ok", "remote", anonymizeRemoteAddr(r.RemoteAddr), "model", req.Model, "messages", len(req.Messages))
		auth.WriteJSON(w, res)
	})))

	listener, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		if addressInUse(err) {
			url := appURL(cfg.Addr)
			appLog.Info("appears to already be running", "app", cfg.AppName, "url", url)
			if err := openBrowser(url); err != nil {
				appLog.Warn("open browser error", "url", url, "error", err)
			}
			return
		}
		appLog.Error("listen failed", "error", err)
		os.Exit(1)
	}
	actualAddr := listener.Addr().String()
	url := appURL(actualAddr)
	appLog.Info("server running", "app", cfg.AppName, "version", appVersion(), "url", url)
	appLog.Info("listening", "addr", actualAddr, "configured", cfg.Addr, "auth", cfg.AuthMode(), "ollama", cfg.OllamaURL, "timeout", cfg.OllamaTimeout)
	if cfg.OpenBrowser {
		go func() {
			if err := openBrowser(url); err != nil {
				appLog.Warn("open browser error", "url", url, "error", err)
			}
		}()
	}

	server := &http.Server{
		Handler:           requestLogger(mux),
		ReadHeaderTimeout: 10 * time.Second,
	}
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- server.Serve(listener)
	}()

	shutdownSignals := make(chan os.Signal, 1)
	signal.Notify(shutdownSignals, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(shutdownSignals)

	select {
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			appLog.Error("serve error", "error", err)
			os.Exit(1)
		}
	case sig := <-shutdownSignals:
		appLog.Info("shutdown signal received", "signal", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			appLog.Warn("graceful shutdown error", "error", err)
			if closeErr := server.Close(); closeErr != nil {
				appLog.Error("forced shutdown error", "error", closeErr)
			}
		}
		if err := <-serveErr; err != nil && !errors.Is(err, http.ErrServerClosed) {
			appLog.Error("serve error after shutdown", "error", err)
			os.Exit(1)
		}
	}
}

func contextBackground() context.Context { return context.Background() }

func appVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "dev"
	}
	var revision, modified string
	for _, setting := range info.Settings {
		switch setting.Key {
		case "vcs.revision":
			revision = setting.Value
		case "vcs.modified":
			modified = setting.Value
		}
	}
	if revision == "" {
		return buildTimeVersion()
	}
	revision = CommitShortName(revision)
	if modified == "true" {
		return revision + "*"
	}
	return revision
}

func buildTimeVersion() string {
	if buildTime != "" {
		return buildTime
	}
	return "dev"
}

func CommitShortName(revision string) string {
	if len(revision) > 7 {
		return revision[:7]
	}
	return revision
}

func watchReloadSignal(app *appRuntime) {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGHUP)
	go func() {
		for range signals {
			cfg, warnings, err := app.Reload(context.Background())
			if err != nil {
				appLog.Error("config reload signal error", "error", err)
				continue
			}
			appLog.Info("config reloaded via signal", "app", cfg.AppName, "auth", cfg.AuthMode(), "ollama", cfg.OllamaURL, "timeout", cfg.OllamaTimeout, "warnings", strings.Join(warnings, "; "))
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
	// Apply DB setting overrides on top of env-var config.
	applyDBSettings(ctx, store, &cfg)

	authm, err := auth.New(ctx, cfg, store)
	if err != nil {
		return nil, err
	}
	tlsCfg, _ := buildTLSConfig(ctx, store)
	return &appRuntime{
		cfg:   cfg,
		authm: authm,
		oc:    ollama.NewWithTLS(cfg.OllamaURL, cfg.OllamaTimeout, tlsCfg),
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

	// Apply DB setting overrides on top of env-var config.
	applyDBSettings(ctx, a.store, &next)

	authm, err := auth.New(ctx, next, a.store)
	if err != nil {
		return current, warnings, err
	}

	tlsCfg, tlsWarn := buildTLSConfig(ctx, a.store)
	if tlsWarn != "" {
		warnings = append(warnings, tlsWarn)
	}
	oc := ollama.NewWithTLS(next.OllamaURL, next.OllamaTimeout, tlsCfg)

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

func (a *appRuntime) Verify(w http.ResponseWriter, r *http.Request) {
	a.Auth().Verify(w, r)
}

func (a *appRuntime) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	a.Auth().ForgotPassword(w, r)
}

func (a *appRuntime) ResetPassword(w http.ResponseWriter, r *http.Request) {
	a.Auth().ResetPassword(w, r)
}

func servePublicStatic(name string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if writeStaticCacheHeaders(w, r, staticFiles(), name) {
			return
		}
		if name == "manifest.webmanifest" {
			w.Header().Set("Content-Type", "application/manifest+json")
		}
		http.ServeFileFS(w, r, staticFiles(), name)
	}
}

func serveManifest(app *appRuntime) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		appName := app.Config().AppName
		if appName == "" {
			appName = "Ollama Chat Tone"
		}
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Content-Type", "application/manifest+json")
		if r.Method == http.MethodHead {
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"name":             appName,
			"short_name":       appName,
			"description":      "A local-first chat client for Ollama.",
			"start_url":        "/",
			"scope":            "/",
			"display":          "standalone",
			"background_color": "#070b13",
			"theme_color":      "#070b13",
			"icons": []map[string]string{
				{
					"src":     "/logo.svg",
					"sizes":   "any",
					"type":    "image/svg+xml",
					"purpose": "any maskable",
				},
			},
		})
	}
}

func staticFileServer(files fs.FS) http.Handler {
	next := http.FileServer(http.FS(files))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		if writeStaticCacheHeaders(w, r, files, staticRequestPath(r.URL.Path)) {
			return
		}
		next.ServeHTTP(w, r)
	})
}

func writeStaticCacheHeaders(w http.ResponseWriter, r *http.Request, files fs.FS, name string) bool {
	etag, ok := staticETag(files, name)
	if !ok {
		w.Header().Set("Cache-Control", "no-cache")
		return false
	}
	w.Header().Set("Cache-Control", "public, max-age=0, must-revalidate")
	w.Header().Set("ETag", etag)
	if etagMatches(r.Header.Get("If-None-Match"), etag) {
		w.WriteHeader(http.StatusNotModified)
		return true
	}
	return false
}

func staticRequestPath(urlPath string) string {
	name := strings.TrimPrefix(path.Clean("/"+urlPath), "/")
	if name == "" || name == "." {
		return "index.html"
	}
	return name
}

func staticETag(files fs.FS, name string) (string, bool) {
	data, err := fs.ReadFile(files, name)
	if err != nil {
		return "", false
	}
	sum := sha256.Sum256(data)
	return `"` + hex.EncodeToString(sum[:]) + `"`, true
}

func etagMatches(header, etag string) bool {
	for part := range strings.SplitSeq(header, ",") {
		if strings.TrimSpace(part) == etag {
			return true
		}
	}
	return false
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

// jobUpdate carries a job's latest state to SSE subscribers.
type jobUpdate struct {
	Status   string `json:"status"`
	Content  string `json:"content"`
	Thinking string `json:"thinking,omitempty"`
	Model    string `json:"model,omitempty"`
	Error    string `json:"error,omitempty"`
}

type jobManager struct {
	mu          sync.Mutex
	cancels     map[string]context.CancelFunc
	subscribers map[string][]chan jobUpdate
}

func newJobManager() *jobManager {
	return &jobManager{
		cancels:     map[string]context.CancelFunc{},
		subscribers: map[string][]chan jobUpdate{},
	}
}

func (m *jobManager) add(id string, cancel context.CancelFunc) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cancels[id] = cancel
}

func (m *jobManager) running(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.cancels[id]
	return ok
}

// remove cleans up the job and closes all SSE subscriber channels.
// It must only be called once the final publish has already been issued,
// so subscribers drain the channel before the close is observed.
func (m *jobManager) remove(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.cancels, id)
	for _, ch := range m.subscribers[id] {
		close(ch)
	}
	delete(m.subscribers, id)
}

func (m *jobManager) cancel(id string) {
	m.mu.Lock()
	cancel := m.cancels[id]
	m.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// jobSubscriberBufSize is the buffer depth for each SSE subscriber channel.
// It is sized to absorb a burst of in-flight chunks between two consecutive
// channel reads without dropping updates.  The channel is non-blocking on send
// (see publish), so a truly slow client will miss intermediate chunks but will
// always receive the final terminal event because the LLM output rate is far
// below this limit in practice.
const jobSubscriberBufSize = 128

// subscribe returns a channel that receives jobUpdate events for the given job.
// If the job is not currently running the returned channel is already closed.
func (m *jobManager) subscribe(id string) chan jobUpdate {
	ch := make(chan jobUpdate, jobSubscriberBufSize)
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.cancels[id]; !ok {
		// Job already finished – return a closed channel so callers exit immediately.
		close(ch)
		return ch
	}
	m.subscribers[id] = append(m.subscribers[id], ch)
	return ch
}

// unsubscribe removes ch from the subscriber list for id.
func (m *jobManager) unsubscribe(id string, ch chan jobUpdate) {
	m.mu.Lock()
	defer m.mu.Unlock()
	subs := m.subscribers[id]
	for i, s := range subs {
		if s == ch {
			m.subscribers[id] = append(subs[:i], subs[i+1:]...)
			return
		}
	}
}

// publish fans out an update to all current subscribers of id.
// Each send is non-blocking: a subscriber whose channel buffer is full will
// miss that intermediate chunk but will still receive the final terminal event
// (complete / error / canceled) because that publish is always followed by
// remove(), which closes the channel and wakes the subscriber.
func (m *jobManager) publish(id string, update jobUpdate) {
	m.mu.Lock()
	if len(m.subscribers[id]) == 0 {
		m.mu.Unlock()
		return
	}
	chs := make([]chan jobUpdate, len(m.subscribers[id]))
	copy(chs, m.subscribers[id])
	m.mu.Unlock()
	for _, ch := range chs {
		select {
		case ch <- update:
		default:
		}
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
		if active, err := store.ActiveChatJob(r.Context(), user, conversationID); err == nil {
			if jobs.running(active.ID) {
				writeError(w, http.StatusConflict, "conversation already has a running response")
				return
			}
			msg := "previous response was interrupted before it could finish"
			if err := retryDBWrite(r.Context(), func(ctx context.Context) error {
				return store.FailChatJob(ctx, user, conversationID, active.ID, msg)
			}); err != nil {
				writeError(w, http.StatusServiceUnavailable, "previous response is still being finalized; try again")
				return
			}
			appLog.Info("cleared orphaned chat job", "user", user, "conversation", conversationID, "job", active.ID)
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
		if !jobs.running(job.ID) {
			msg := "previous response was interrupted before it could finish"
			if err := retryDBWrite(r.Context(), func(ctx context.Context) error {
				return store.FailChatJob(ctx, user, conversationID, job.ID, msg)
			}); err != nil {
				writeError(w, http.StatusServiceUnavailable, "previous response is still being finalized; try again")
				return
			}
			auth.WriteJSON(w, map[string]any{"job": nil})
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
		if len(parts) == 2 && parts[1] == "events" && r.Method == http.MethodGet {
			handleJobEvents(w, r, store, jobs, user, conversationID, jobID)
			return
		}
		if len(parts) == 2 && parts[1] == "cancel" && r.Method == http.MethodPost {
			jobs.cancel(jobID)
			if err := store.CancelChatJob(r.Context(), user, conversationID, jobID); err != nil {
				writeDBError(w, err, "job not found")
				return
			}
			job, err := store.GetChatJob(r.Context(), user, conversationID, jobID)
			if err != nil {
				writeDBError(w, err, "job not found")
				return
			}
			jobs.publish(jobID, jobUpdate{Status: job.Status, Content: job.Content, Thinking: job.Thinking, Model: job.Model, Error: job.Error})
			auth.WriteJSON(w, map[string]any{"canceled": true, "job": job})
			return
		}
	}

	writeError(w, http.StatusNotFound, "job not found")
}

// handleJobEvents streams real-time job updates to the client via Server-Sent Events.
// The client opens a long-lived GET connection and receives a data event for every
// chunk published by the background job, plus a final event when the job finishes.
func handleJobEvents(w http.ResponseWriter, r *http.Request, store *db.Store, jobs *jobManager, user, conversationID, jobID string) {
	rc := http.NewResponseController(w)

	// Subscribe BEFORE reading the DB so we cannot miss a publish that lands
	// between the DB read and us starting to wait on the channel.
	ch := jobs.subscribe(jobID)
	defer jobs.unsubscribe(jobID, ch)

	job, err := store.GetChatJob(r.Context(), user, conversationID, jobID)
	if err != nil {
		writeError(w, http.StatusNotFound, "job not found")
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx buffering
	w.WriteHeader(http.StatusOK)

	// Send the current snapshot so the client is never stuck waiting for the
	// first update even if the job is already partially or fully complete.
	writeSSEJobData(w, rc, job)
	if job.Status != "running" {
		return
	}

	for {
		select {
		case <-r.Context().Done():
			return
		case update, ok := <-ch:
			if !ok {
				// Channel was closed by remove() after the final publish;
				// the client already received the terminal event.
				return
			}
			writeSSEJobData(w, rc, &db.ChatJob{
				ID:             job.ID,
				ConversationID: job.ConversationID,
				Status:         update.Status,
				Content:        update.Content,
				Thinking:       update.Thinking,
				Model:          update.Model,
				Error:          update.Error,
			})
			if update.Status != "running" {
				return
			}
		}
	}
}

// writeSSEJobData serialises job as a plain SSE data event and flushes.
func writeSSEJobData(w http.ResponseWriter, rc *http.ResponseController, job *db.ChatJob) {
	b, err := json.Marshal(job)
	if err != nil {
		return
	}
	fmt.Fprintf(w, "data: %s\n\n", b)
	_ = rc.Flush()
}

func runChatJob(ctx context.Context, store *db.Store, oc *ollama.Client, jobs *jobManager, user, conversationID, jobID, model string, messages []ollama.Message, think bool) {
	defer jobs.remove(jobID)
	content := ""
	thinking := ""
	lastPersist := time.Time{}
	req := ollama.ChatRequest{Model: model, Messages: messages, Stream: true, Think: &think}
	err := oc.StreamChat(ctx, req, func(chunk ollama.ChatResponse) error {
		content += chunk.Message.Content
		if think {
			thinking += chunk.Message.Thinking
		}
		jobs.publish(jobID, jobUpdate{Status: "running", Content: content, Thinking: thinking, Model: model})
		if time.Since(lastPersist) < 500*time.Millisecond {
			return nil
		}
		lastPersist = time.Now()
		if err := store.UpdateChatJob(context.Background(), user, conversationID, jobID, content, thinking); err != nil {
			if db.IsBusy(err) {
				appLog.Debug("runChatJob: skipped busy progress update", "job", jobID, "error", err)
				return nil
			}
			return err
		}
		return nil
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			jobs.publish(jobID, jobUpdate{Status: "canceled", Content: content, Thinking: thinking, Model: model})
			if cancelErr := retryDBWrite(context.Background(), func(ctx context.Context) error {
				return store.CancelChatJob(ctx, user, conversationID, jobID)
			}); cancelErr != nil {
				appLog.Warn("runChatJob: cancel job error", "job", jobID, "error", cancelErr)
			}
			return
		}
		jobs.publish(jobID, jobUpdate{Status: "error", Content: content, Thinking: thinking, Model: model, Error: err.Error()})
		if failErr := retryDBWrite(context.Background(), func(ctx context.Context) error {
			return store.FailChatJob(ctx, user, conversationID, jobID, err.Error())
		}); failErr != nil {
			appLog.Warn("runChatJob: fail job error", "job", jobID, "error", failErr)
		}
		return
	}
	err = retryDBWrite(context.Background(), func(ctx context.Context) error {
		_, err := store.CompleteChatJob(ctx, user, conversationID, jobID, content, thinking, model)
		return err
	})
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			if job, getErr := store.GetChatJob(context.Background(), user, conversationID, jobID); getErr == nil && job.Status != "running" {
				jobs.publish(jobID, jobUpdate{Status: job.Status, Content: job.Content, Thinking: job.Thinking, Model: job.Model, Error: job.Error})
				return
			}
		}
		jobs.publish(jobID, jobUpdate{Status: "error", Content: content, Thinking: thinking, Model: model, Error: err.Error()})
		if failErr := retryDBWrite(context.Background(), func(ctx context.Context) error {
			return store.FailChatJob(ctx, user, conversationID, jobID, err.Error())
		}); failErr != nil {
			appLog.Warn("runChatJob: fail job after complete error", "job", jobID, "error", failErr)
		}
		return
	}
	jobs.publish(jobID, jobUpdate{Status: "complete", Content: content, Thinking: thinking, Model: model})
}

func retryDBWrite(parent context.Context, fn func(context.Context) error) error {
	var lastErr error
	for attempt := 0; attempt < 6; attempt++ {
		ctx, cancel := context.WithTimeout(parent, 8*time.Second)
		err := fn(ctx)
		cancel()
		if err == nil {
			return nil
		}
		lastErr = err
		if !db.IsBusy(err) {
			return err
		}
		select {
		case <-parent.Done():
			return parent.Err()
		case <-time.After(time.Duration(attempt+1) * 250 * time.Millisecond):
		}
	}
	return lastErr
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
		requestID := r.Header.Get("X-Request-ID")
		user := userFromRequest(r)
		entry := appLog.With("method", r.Method, "path", r.URL.Path, "status", lw.status, "remote", anonymizeRemoteAddr(r.RemoteAddr))
		if requestID != "" {
			entry = entry.With("requestID", requestID)
		}
		if user != "" {
			entry = entry.With("user", user)
		}
		if lw.status >= http.StatusInternalServerError {
			entry.Error("request complete")
			return
		}
		if lw.status >= http.StatusBadRequest {
			entry.Warn("request complete")
			return
		}
		entry.Info("request complete")
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
				appLog.Debug("chat stream includes thinking", "remote", anonymizeRemoteAddr(r.RemoteAddr), "model", req.Model)
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

	appLog.Info("chat stream ok", "remote", anonymizeRemoteAddr(r.RemoteAddr), "model", req.Model, "messages", len(req.Messages), "chunks", chunks, "thinkingChunks", thinkingChunks)
	return nil
}

func anonymizeRemoteAddr(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return host
	}
	if ipv4 := ip.To4(); ipv4 != nil {
		return net.IPv4(ipv4[0], ipv4[1], ipv4[2], 0).String()
	}
	ip = ip.To16()
	if ip == nil {
		return host
	}
	return ip.Mask(net.CIDRMask(64, 128)).String()
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
		appLog.Error("failed to load static files", "error", err)
		os.Exit(1)
	}
	return sub
}

// applyDBSettings overlays admin-managed settings stored in DB onto cfg.
func applyDBSettings(ctx context.Context, store *db.Store, cfg *config.Config) {
	if v := store.GetSetting(ctx, "ollama_url", ""); v != "" {
		cfg.OllamaURL = v
	}
	if v := store.GetSetting(ctx, "ollama_timeout", ""); v != "" {
		if d, err := time.ParseDuration(v); err == nil {
			cfg.OllamaTimeout = d
		} else if mins, err := strconv.Atoi(v); err == nil {
			cfg.OllamaTimeout = time.Duration(mins) * time.Minute
		}
	}
	if v := store.GetSetting(ctx, "ollama_default_model", ""); v != "" {
		cfg.DefaultModel = v
	}
}

// buildTLSConfig loads a mTLS config from DB settings, if a PFX cert is stored.
func buildTLSConfig(ctx context.Context, store *db.Store) (*tls.Config, string) {
	pfxB64 := store.GetSetting(ctx, "ollama_tls_pfx", "")
	if pfxB64 == "" {
		return nil, ""
	}
	pfxBytes, err := base64.StdEncoding.DecodeString(pfxB64)
	if err != nil {
		return nil, "mTLS: could not decode stored PFX: " + err.Error()
	}
	passphrase := store.GetSetting(ctx, "ollama_tls_pfx_pass", "")
	tlsCfg, warn := buildTLSFromPFX(pfxBytes, passphrase)
	return tlsCfg, warn
}

func buildTLSFromPFX(pfxBytes []byte, passphrase string) (*tls.Config, string) {
	cert, warn := parseTLSCert(pfxBytes, passphrase)
	if warn != "" {
		return nil, warn
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}}, ""
}

func parseTLSCert(pfxBytes []byte, passphrase string) (tls.Certificate, string) {
	privKey, leaf, err := parsePFX(pfxBytes, passphrase)
	if err != nil {
		return tls.Certificate{}, "mTLS: could not parse PFX: " + err.Error()
	}
	cert := tls.Certificate{
		Certificate: [][]byte{leaf.Raw},
		PrivateKey:  privKey,
		Leaf:        leaf,
	}
	return cert, ""
}

// parsePFX decodes a PKCS#12 bundle and returns the private key and leaf certificate.
func parsePFX(pfxBytes []byte, passphrase string) (any, *x509.Certificate, error) {
	priv, cert, err := pkcs12.Decode(pfxBytes, passphrase)
	if err != nil {
		return nil, nil, err
	}
	return priv, cert, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
