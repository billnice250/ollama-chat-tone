package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/billnice250/ollama-chat-tone/internal/config"
	"github.com/billnice250/ollama-chat-tone/internal/db"
)

func TestRequireAuthAcceptsSignedSessionForOIDCUser(t *testing.T) {
	ctx := context.Background()
	store, err := db.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.DB.Close()

	user, err := store.EnsurePendingUser(ctx, "user@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if !user.EmailVerified {
		t.Fatal("OIDC user should be marked email verified")
	}
	if err := store.ApproveUser(ctx, user.Username); err != nil {
		t.Fatal(err)
	}

	manager := &Manager{
		cfg: config.Config{
			SessionSecret:    "0123456789abcdef0123456789abcdef",
			OIDCIssuer:       "https://issuer.example.com",
			OIDCClientID:     "client-id",
			OIDCClientSecret: "client-secret",
		},
		store: store,
	}

	called := false
	handler := manager.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		if got := r.Context().Value(EmailKey); got != "user@example.com" {
			t.Fatalf("context email = %v, want user@example.com", got)
		}
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookie, Value: manager.signSession(user.Username), Path: "/"})
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if !called {
		t.Fatal("authenticated handler was not called")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}
