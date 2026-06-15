package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"embed"
	"encoding/base64"
	"encoding/json"
	"html/template"
	"net/http"
	"strings"

	"github.com/billnice250/ollama-chat-tone/internal/config"
	"github.com/billnice250/ollama-chat-tone/internal/db"
	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/crypto/bcrypt"
	"golang.org/x/oauth2"
)

//go:embed templates/*.html
var templateFS embed.FS

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
	templates    *template.Template
}

func New(ctx context.Context, cfg config.Config, store *db.Store) (*Manager, error) {
	tmpl, err := template.ParseFS(templateFS, "templates/*.html")
	if err != nil {
		return nil, err
	}
	m := &Manager{cfg: cfg, store: store, templates: tmpl}
	if localAvailable(cfg) {
		hash, err := bcrypt.GenerateFromPassword([]byte(cfg.BasicPass), bcrypt.DefaultCost)
		if err != nil {
			return nil, err
		}
		if err := store.EnsureAdmin(ctx, strings.ToLower(cfg.BasicUser), string(hash)); err != nil {
			return nil, err
		}
	}
	if oidcAvailable(cfg) {
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
		if m.cfg.AuthMode() == "none" {
			ctx := context.WithValue(r.Context(), EmailKey, "anonymous")
			ctx = context.WithValue(ctx, AdminKey, false)
			next.ServeHTTP(w, r.WithContext(ctx))
			return
		}

		if localAvailable(m.cfg) {
			username, ok := m.readSession(r)
			if ok {
				user, err := m.store.GetUser(r.Context(), username)
				if err == nil && user.Approved {
					ctx := context.WithValue(r.Context(), EmailKey, user.Username)
					ctx = context.WithValue(ctx, AdminKey, user.IsAdmin)
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
				clearCookie(w, sessionCookie)
			}
		}

		if oidcAvailable(m.cfg) {
			email := readCookie(r, "email")
			if email != "" {
				user, err := m.store.GetUser(r.Context(), email)
				if err == nil && user.Approved {
					ctx := context.WithValue(r.Context(), EmailKey, user.Username)
					ctx = context.WithValue(ctx, AdminKey, user.IsAdmin)
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}
				clearCookie(w, "email")
			}
		}

		http.Redirect(w, r, "/auth/login", http.StatusFound)
	})
}

func (m *Manager) Login(w http.ResponseWriter, r *http.Request) {
	if m.cfg.AuthMode() == "none" {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	if r.URL.Query().Get("provider") == "oidc" && oidcAvailable(m.cfg) && m.oauth2Config != nil {
		m.startOIDC(w, r)
		return
	}
	if localAvailable(m.cfg) {
		m.localLogin(w, r)
		return
	}

	if r.Method == http.MethodGet {
		m.writeAuthPage(w, authPageData{
			AppName:    m.cfg.AppName,
			Title:      "Log in",
			ShowOAuth:  m.oauth2Config != nil,
			OAuthHref:  "/auth/login?provider=oidc",
			OAuthLabel: "Continue with OAuth",
		})
		return
	}
	http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
}

func (m *Manager) startOIDC(w http.ResponseWriter, r *http.Request) {
	state := randomState()
	setCookie(w, "state", state, true)
	http.Redirect(w, r, m.oauth2Config.AuthCodeURL(state), http.StatusFound)
}

func (m *Manager) Signup(w http.ResponseWriter, r *http.Request) {
	if !localAvailable(m.cfg) {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodGet {
		m.writeLocalAuthPage(w, "Register", "", true)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	username := strings.ToLower(strings.TrimSpace(r.FormValue("username")))
	password := r.FormValue("password")
	if username == "" || password == "" {
		m.writeLocalAuthPage(w, "Register", "username and password are required", true)
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := m.store.CreateUser(r.Context(), username, string(hash)); err != nil {
		m.writeLocalAuthPage(w, "Register", "user already exists or cannot be created", true)
		return
	}
	m.writeLocalAuthPage(w, "Register", "request submitted; wait for an admin to approve access", true)
}

func (m *Manager) Callback(w http.ResponseWriter, r *http.Request) {
	if m.oauth2Config == nil || m.verifier == nil {
		http.NotFound(w, r)
		return
	}
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
	if email == "" {
		http.Error(w, "missing email claim", 500)
		return
	}
	user, err := m.store.EnsurePendingUser(r.Context(), email)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	if !user.Approved {
		clearCookie(w, "email")
		m.writeAuthPage(w, authPageData{
			AppName:    m.cfg.AppName,
			Title:      "Approval pending",
			Message:    "Your account was registered and is waiting for admin approval.",
			ShowOAuth:  true,
			OAuthHref:  "/auth/login?provider=oidc",
			OAuthLabel: "Try again",
		})
		return
	}
	setCookie(w, "email", email, false)
	http.Redirect(w, r, "/", http.StatusFound)
}

func (m *Manager) Logout(w http.ResponseWriter, r *http.Request) {
	clearCookie(w, "email")
	if localAvailable(m.cfg) {
		clearCookie(w, sessionCookie)
	}
	if m.cfg.AuthMode() != "none" {
		m.writeLogoutPage(w)
		return
	}
	http.Redirect(w, r, "/", http.StatusFound)
}

func (m *Manager) localLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		m.writeLocalAuthPage(w, "Log in", "", false)
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
		m.writeLocalAuthPage(w, "Log in", "invalid username or password", false)
		return
	}
	if !user.Approved {
		m.writeLocalAuthPage(w, "Log in", "your signup is waiting for admin approval", false)
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

func localAvailable(cfg config.Config) bool {
	return cfg.BasicUser != "" && cfg.BasicPass != ""
}

func oidcAvailable(cfg config.Config) bool {
	return cfg.OIDCIssuer != "" && cfg.OIDCClientID != "" && cfg.OIDCClientSecret != ""
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

type authPageData struct {
	AppName              string
	Title                string
	Message              string
	Action               string
	AltHref              string
	AltText              string
	PasswordAutocomplete string
	ShowLocal            bool
	ShowOAuth            bool
	OAuthHref            string
	OAuthLabel           string
}

type logoutPageData struct {
	AppName string
}

func (m *Manager) writeLocalAuthPage(w http.ResponseWriter, title, message string, signup bool) {
	action := "/auth/login"
	altHref := "/auth/signup"
	altText := "Register"
	autocomplete := "current-password"
	if signup {
		action = "/auth/signup"
		altHref = "/auth/login"
		altText = "Back to login"
		autocomplete = "new-password"
	}
	m.writeAuthPage(w, authPageData{
		AppName:              m.cfg.AppName,
		Title:                title,
		Message:              message,
		Action:               action,
		AltHref:              altHref,
		AltText:              altText,
		PasswordAutocomplete: autocomplete,
		ShowLocal:            true,
		ShowOAuth:            m.oauth2Config != nil,
		OAuthHref:            "/auth/login?provider=oidc",
		OAuthLabel:           "Continue with OAuth",
	})
}

func (m *Manager) writeAuthPage(w http.ResponseWriter, data authPageData) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := m.templates.ExecuteTemplate(w, "auth.html", data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

func (m *Manager) writeLogoutPage(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	if err := m.templates.ExecuteTemplate(w, "logout.html", logoutPageData{AppName: m.cfg.AppName}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
