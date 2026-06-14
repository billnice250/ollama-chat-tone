package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"html"
	"net/http"
	"strings"

	"github.com/billnice250/ollama-chat-client/internal/config"
	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

type ctxKey string

const EmailKey ctxKey = "email"
const basicLoggedOutCookie = "basic_logged_out"

type Manager struct {
	cfg          config.Config
	provider     *oidc.Provider
	verifier     *oidc.IDTokenVerifier
	oauth2Config *oauth2.Config
}

func New(ctx context.Context, cfg config.Config) (*Manager, error) {
	m := &Manager{cfg: cfg}
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
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), EmailKey, "anonymous")))
		case "basic":
			if readCookie(r, basicLoggedOutCookie) == "1" {
				w.Header().Set("WWW-Authenticate", basicRealm(m.cfg.AppName))
				http.Error(w, "logged out", http.StatusUnauthorized)
				return
			}
			u, p, ok := r.BasicAuth()
			if !ok || u != m.cfg.BasicUser || p != m.cfg.BasicPass {
				w.Header().Set("WWW-Authenticate", basicRealm(m.cfg.AppName))
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), EmailKey, u)))
		case "oidc":
			email := readCookie(r, "email")
			if email == "" {
				http.Redirect(w, r, "/auth/login", http.StatusFound)
				return
			}
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), EmailKey, email)))
		}
	})
}

func (m *Manager) Login(w http.ResponseWriter, r *http.Request) {
	if m.cfg.AuthMode() == "basic" {
		http.SetCookie(w, &http.Cookie{Name: basicLoggedOutCookie, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode})
		http.Redirect(w, r, "/", http.StatusFound)
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
	if m.cfg.AuthMode() == "basic" {
		http.SetCookie(w, &http.Cookie{Name: basicLoggedOutCookie, Value: "1", Path: "/", MaxAge: 86400 * 30, HttpOnly: true, SameSite: http.SameSiteLaxMode})
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		name := html.EscapeString(m.cfg.AppName)
		_, _ = w.Write([]byte(`<!doctype html><title>Logged out</title><body style="font-family:system-ui;margin:40px"><h1>Logged out</h1><p>You are logged out of ` + name + `.</p><p><a href="/auth/login">Log in again</a></p></body>`))
		return
	}
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

func basicRealm(name string) string {
	name = strings.ReplaceAll(name, `\`, `\\`)
	name = strings.ReplaceAll(name, `"`, `\"`)
	return `Basic realm="` + name + `"`
}
