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
	"log"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/billnice250/ollama-chat-tone/internal/config"
	"github.com/billnice250/ollama-chat-tone/internal/db"
	"github.com/billnice250/ollama-chat-tone/internal/mailer"
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

var emailRE = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

type Manager struct {
	cfg          config.Config
	store        *db.Store
	mailer       *mailer.Mailer
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
	ml := mailer.New(cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPUser, cfg.SMTPPass, cfg.SMTPFrom)
	m := &Manager{cfg: cfg, store: store, mailer: ml, templates: tmpl}
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

		username, ok := m.readSession(r)
		if ok {
			user, err := m.store.GetUser(r.Context(), username)
			if err == nil && user.Approved && user.EmailVerified {
				_ = m.store.TouchUser(r.Context(), user.Username)
				ctx := context.WithValue(r.Context(), EmailKey, user.Username)
				ctx = context.WithValue(ctx, AdminKey, user.IsAdmin)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}
		}
		// Clear any stale or invalid session cookie (failed HMAC, missing user,
		// or account not yet approved/verified) so it doesn't keep redirecting.
		if readCookie(r, sessionCookie) != "" {
			clearCookie(w, sessionCookie)
		}

		if oidcAvailable(m.cfg) {
			email := readCookie(r, "email")
			if email != "" {
				user, err := m.store.GetUser(r.Context(), email)
				if err == nil && user.Approved {
					_ = m.store.TouchUser(r.Context(), user.Username)
					setCookie(w, sessionCookie, m.signSession(user.Username), false)
					clearCookie(w, "email")
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
		m.writeLocalAuthPage(w, "Register", "email and password are required", true)
		return
	}
	if !emailRE.MatchString(username) {
		m.writeLocalAuthPage(w, "Register", "a valid email address is required", true)
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

	// Send email verification link if mailer is available.
	if m.mailer.Available() {
		tok, err := m.store.CreateToken(r.Context(), username, "verify", 24*time.Hour)
		if err == nil {
			link := m.absURL("/auth/verify?token=" + tok)
			_ = m.mailer.Send(username, "Verify your email – "+m.cfg.AppName,
				"Click the link below to verify your email address:\n\n"+link+"\n\nThis link expires in 24 hours.")
		}
		m.writeLocalAuthPage(w, "Register", "check your email for a verification link", true)
		return
	}

	// No mailer: auto-verify and let admin approve.
	_ = m.store.VerifyEmail(r.Context(), username)
	m.writeLocalAuthPage(w, "Register", "request submitted; wait for an admin to approve access", true)
}

// Verify handles the email verification link.
func (m *Manager) Verify(w http.ResponseWriter, r *http.Request) {
	tok := r.URL.Query().Get("token")
	if tok == "" {
		m.writeSimplePage(w, "Verify email", "Missing verification token.")
		return
	}
	username, err := m.store.ConsumeToken(r.Context(), tok, "verify")
	if err != nil {
		m.writeSimplePage(w, "Verify email", "The verification link is invalid or has expired.")
		return
	}
	_ = m.store.VerifyEmail(r.Context(), username)
	m.writeSimplePage(w, "Email verified", "Your email has been verified. An admin will approve your access shortly.")
}

// ForgotPassword shows / handles the forgot-password form.
func (m *Manager) ForgotPassword(w http.ResponseWriter, r *http.Request) {
	if !localAvailable(m.cfg) {
		http.NotFound(w, r)
		return
	}
	if r.Method == http.MethodGet {
		m.writeAuthPage(w, authPageData{
			AppName:        m.cfg.AppName,
			Title:          "Forgot password",
			Action:         "/auth/forgot-password",
			AltHref:        "/auth/login",
			AltText:        "Back to login",
			ShowForgotForm: true,
		})
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	email := strings.ToLower(strings.TrimSpace(r.FormValue("email")))
	// Always show the same message to prevent user enumeration.
	msg := "If an account with that email exists, a reset link has been sent."
	if email != "" && m.mailer.Available() {
		if _, err := m.store.GetUser(r.Context(), email); err == nil {
			tok, err := m.store.CreateToken(r.Context(), email, "reset", time.Hour)
			if err == nil {
				link := m.absURL("/auth/reset-password?token=" + tok)
				_ = m.mailer.Send(email, "Reset your password – "+m.cfg.AppName,
					"Click the link below to reset your password:\n\n"+link+"\n\nThis link expires in 1 hour.")
			}
		}
	}
	m.writeAuthPage(w, authPageData{
		AppName: m.cfg.AppName,
		Title:   "Forgot password",
		Message: msg,
		AltHref: "/auth/login",
		AltText: "Back to login",
	})
}

// ResetPassword handles the password-reset form.
func (m *Manager) ResetPassword(w http.ResponseWriter, r *http.Request) {
	if !localAvailable(m.cfg) {
		http.NotFound(w, r)
		return
	}
	tok := r.URL.Query().Get("token")
	if r.Method == http.MethodGet {
		if tok == "" {
			http.Redirect(w, r, "/auth/forgot-password", http.StatusFound)
			return
		}
		m.writeAuthPage(w, authPageData{
			AppName:       m.cfg.AppName,
			Title:         "Reset password",
			Action:        "/auth/reset-password?token=" + tok,
			ShowResetForm: true,
		})
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if tok == "" {
		tok = r.FormValue("token")
	}
	password := r.FormValue("password")
	if password == "" {
		m.writeAuthPage(w, authPageData{
			AppName:       m.cfg.AppName,
			Title:         "Reset password",
			Message:       "Password is required.",
			Action:        "/auth/reset-password?token=" + tok,
			ShowResetForm: true,
		})
		return
	}
	username, err := m.store.ConsumeToken(r.Context(), tok, "reset")
	if err != nil {
		m.writeSimplePage(w, "Reset password", "The reset link is invalid or has expired. Please request a new one.")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := m.store.SetPassword(r.Context(), username, string(hash)); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	m.writeSimplePage(w, "Password reset", "Your password has been reset. You can now log in.")
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
		log.Printf("auth: OIDC login denied for %q: account not approved", email)
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
	setCookie(w, sessionCookie, m.signSession(user.Username), false)
	clearCookie(w, "email")
	clearCookie(w, "state")
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
		log.Printf("auth: login failed for %q: invalid credentials", username)
		m.writeLocalAuthPage(w, "Log in", "invalid email or password", false)
		return
	}
	if !user.EmailVerified {
		log.Printf("auth: login failed for %q: email not verified", username)
		m.writeLocalAuthPage(w, "Log in", "please verify your email address before logging in", false)
		return
	}
	if !user.Approved {
		log.Printf("auth: login failed for %q: account not approved", username)
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
	i := strings.LastIndex(v, ".")
	if i <= 0 || i == len(v)-1 {
		return "", false
	}
	username := v[:i]
	return username, hmac.Equal([]byte(m.signSession(username)), []byte(v))
}

func (m *Manager) absURL(path string) string {
	base := strings.TrimRight(m.cfg.BaseURL, "/")
	if base == "" {
		base = "http://localhost" + m.cfg.Addr
	}
	return base + path
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
	ShowForgotLink       bool
	ShowForgotForm       bool
	ShowResetForm        bool
}

type logoutPageData struct {
	AppName string
}

type simplePageData struct {
	AppName string
	Title   string
	Message string
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
		ShowForgotLink:       !signup && m.mailer.Available(),
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

func (m *Manager) writeSimplePage(w http.ResponseWriter, title, message string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := m.templates.ExecuteTemplate(w, "simple.html", simplePageData{AppName: m.cfg.AppName, Title: title, Message: message}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
