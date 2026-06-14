package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"strings"

	"github.com/billnice250/ollama-chat-client/internal/config"
	"github.com/billnice250/ollama-chat-client/internal/db"
	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/oauth2"
)

type ctxKey string

const EmailKey ctxKey = "email"
const AdminKey ctxKey = "admin"

const sessionCookie = "session"

type Manager struct {
	cfg          config.Config
	store        *db.Store
	provider     *oidc.Provider
	verifier     *oidc.IDTokenVerifier
	oauth2Config *oauth2.Config
}

func New(ctx context.Context, cfg config.Config, store *db.Store) (*Manager, error) {
	m := &Manager{cfg: cfg, store: store}
	if cfg.AuthMode() == "local" {
		hash, err := bcrypt.GenerateFromPassword([]byte(cfg.BasicPass), bcrypt.DefaultCost)
		if err != nil {
			return nil, err
		}
		if err := store.EnsureAdmin(ctx, strings.ToLower(cfg.BasicUser), string(hash)); err != nil {
			return nil, err
		}
	}
	if cfg.AuthMode() == "oidc" {
		p, err := oidc.NewProvider(ctx, cfg.OIDCIssuer)
		if err != nil {
			return nil, err
		}
		m.provider = p
		m.verifier = p.Verifier(&oidc.Config{ClientID: cfg.OIDCClientID})
		m.oauth2Config = &oauth2.Config{
			ClientID: cfg.OIDCClientID, ClientSecret: cfg.OIDCClientSecret,
			RedirectURL: cfg.OIDCRedirectURL,
			Endpoint:    p.Endpoint(), Scopes: []string{oidc.ScopeOpenID, "email", "profile"},
		}
	}
	return m, nil
}

func (m *Manager) RequireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch m.cfg.AuthMode() {
		case "none":
			ctx := context.WithValue(r.Context(), EmailKey, "anonymous")
			ctx = context.WithValue(ctx, AdminKey, false)
			next.ServeHTTP(w, r.WithContext(ctx))
		case "local":
			username, ok := m.readSession(r)
			if !ok {
				http.Redirect(w, r, "/auth/login", http.StatusFound)
				return
			}
			user, err := m.store.GetUser(r.Context(), username)
			if err != nil || !user.Approved {
				clearCookie(w, sessionCookie)
				http.Redirect(w, r, "/auth/login", http.StatusFound)
				return
			}
			ctx := context.WithValue(r.Context(), EmailKey, user.Username)
			ctx = context.WithValue(ctx, AdminKey, user.IsAdmin)
			next.ServeHTTP(w, r.WithContext(ctx))
		case "oidc":
			email := readCookie(r, "email")
			if email == "" {
				http.Redirect(w, r, "/auth/login", http.StatusFound)
				return
			}
			ctx := context.WithValue(r.Context(), EmailKey, email)
			ctx = context.WithValue(ctx, AdminKey, false)
			next.ServeHTTP(w, r.WithContext(ctx))
		}
	})
}

func (m *Manager) Login(w http.ResponseWriter, r *http.Request) {
	if m.cfg.AuthMode() == "local" {
		m.localLogin(w, r)
		return
	}
	if m.cfg.AuthMode() == "none" {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	state := randomState()
	setCookie(w, "state", state, true)
	http.Redirect(w, r, m.oauth2Config.AuthCodeURL(state), http.StatusFound)
}

func (m *Manager) Signup(w http.ResponseWriter, r *http.Request) {
	if m.cfg.AuthMode() != "local" {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodGet {
		writeAuthPage(w, m.cfg.AppName, "Sign up", "", true)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	username := strings.ToLower(strings.TrimSpace(r.FormValue("username")))
	password := r.FormValue("password")
	if username == "" || password == "" {
		writeAuthPage(w, m.cfg.AppName, "Sign up", "username and password are required", true)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := m.store.CreateUser(r.Context(), username, string(hash)); err != nil {
		writeAuthPage(w, m.cfg.AppName, "Sign up", "user already exists or cannot be created", true)
		return
	}
	writeAuthPage(w, m.cfg.AppName, "Sign up", "request submitted; wait for an admin to approve access", true)
}

func (m *Manager) Callback(w http.ResponseWriter, r *http.Request) {
	if r.URL.Query().Get("state") != readCookie(r, "state") {
		http.Error(w, "bad state", 400)
		return
	}
	tok, err := m.oauth2Config.Exchange(r.Context(), r.URL.Query().Get("code"))
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	rawID, ok := tok.Extra("id_token").(string)
	if !ok {
		http.Error(w, "missing id_token", 500)
		return
	}
	idToken, err := m.verifier.Verify(r.Context(), rawID)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	var claims struct {
		Email string `json:"email"`
	}
	if err := idToken.Claims(&claims); err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	email := strings.ToLower(claims.Email)
	if len(m.cfg.AllowedEmails) > 0 && !m.cfg.AllowedEmails[email] {
		http.Error(w, "email not allowed", 403)
		return
	}
	setCookie(w, "email", email, false)
	http.Redirect(w, r, "/", http.StatusFound)
}

func (m *Manager) Logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "email", Value: "", Path: "/", MaxAge: -1})
	if m.cfg.AuthMode() == "local" {
		clearCookie(w, sessionCookie)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		name := html.EscapeString(m.cfg.AppName)
		_, _ = w.Write([]byte(`<!doctype html><title>Logged out</title><body style="font-family:system-ui;margin:40px"><h1>Logged out</h1><p>You are logged out of ` + name + `.</p><p><a href="/auth/login">Log in again</a></p></body>`))
		return
	}
	http.Redirect(w, r, "/", http.StatusFound)
}

func (m *Manager) localLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		writeAuthPage(w, m.cfg.AppName, "Log in", "", false)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	username := strings.ToLower(strings.TrimSpace(r.FormValue("username")))
	password := r.FormValue("password")
	user, err := m.store.GetUser(r.Context(), username)
	if err != nil || bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(password)) != nil {
		writeAuthPage(w, m.cfg.AppName, "Log in", "invalid username or password", false)
		return
	}
	if !user.Approved {
		writeAuthPage(w, m.cfg.AppName, "Log in", "your signup is waiting for admin approval", false)
		return
	}
	setCookie(w, sessionCookie, m.signSession(user.Username), false)
	http.Redirect(w, r, "/", http.StatusFound)
}

func setCookie(w http.ResponseWriter, name, value string, short bool) {
	max := 86400 * 30
	if short {
		max = 600
	}
	http.SetCookie(w, &http.Cookie{Name: name, Value: value, Path: "/", MaxAge: max, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}
func readCookie(r *http.Request, name string) string {
	c, err := r.Cookie(name)
	if err != nil {
		return ""
	}
	return c.Value
}
func randomState() string {
	b := make([]byte, 32)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
func WriteJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func clearCookie(w http.ResponseWriter, name string) {
	http.SetCookie(w, &http.Cookie{Name: name, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
}

func (m *Manager) signSession(username string) string {
	mac := hmac.New(sha256.New, []byte(m.cfg.SessionSecret))
	mac.Write([]byte(username))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return username + "." + sig
}

func (m *Manager) readSession(r *http.Request) (string, bool) {
	v := readCookie(r, sessionCookie)
	username, sig, ok := strings.Cut(v, ".")
	if !ok || username == "" {
		return "", false
	}
	return username, hmac.Equal([]byte(m.signSession(username)), []byte(username+"."+sig))
}

func writeAuthPage(w http.ResponseWriter, appName, title, message string, signup bool) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	action := "/auth/login"
	altHref := "/auth/signup"
	altText := "Request access"
	if signup {
		action = "/auth/signup"
		altHref = "/auth/login"
		altText = "Back to login"
	}
	msg := ""
	if message != "" {
		msg = `<p class="message">` + html.EscapeString(message) + `</p>`
	}
	_, _ = fmt.Fprintf(w, `<!doctype html>
<html><head><meta name="viewport" content="width=device-width,initial-scale=1"><title>%s</title>
<style>body{font-family:system-ui;margin:0;min-height:100vh;display:grid;place-items:center;background:#f6f7f9;color:#111827}.box{width:min(420px,92vw);background:white;border:1px solid #d9dee7;border-radius:8px;padding:24px}input,button{font:inherit;width:100%%;box-sizing:border-box;height:40px;margin:7px 0}input{border:1px solid #d9dee7;border-radius:8px;padding:0 10px}button{border:0;border-radius:8px;background:#0f766e;color:white;font-weight:700}.message{color:#b42318}a{color:#134e4a}</style></head>
<body><form class="box" method="post" action="%s"><h1>%s</h1><p>%s</p>%s<input name="username" placeholder="Username" autocomplete="username" required><input name="password" type="password" placeholder="Password" autocomplete="current-password" required><button type="submit">%s</button><p><a href="%s">%s</a></p></form></body></html>`,
		html.EscapeString(title), action, html.EscapeString(title), html.EscapeString(appName), msg, html.EscapeString(title), altHref, altText)
}
