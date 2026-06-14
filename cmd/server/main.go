package main

import (
	"context"
	"encoding/json"
	"log"
	"net/http"

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
	mux.Handle("/", authm.RequireAuth(http.FileServer(http.Dir("web"))))
	mux.Handle("/api/chat", authm.RequireAuth(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", 405)
			return
		}
		var req ollama.ChatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "bad request", 400)
			return
		}
		if req.Model == "" {
			req.Model = cfg.DefaultModel
		}
		res, err := oc.Chat(r.Context(), req)
		if err != nil {
			http.Error(w, err.Error(), 502)
			return
		}
		auth.WriteJSON(w, res)
	})))

	log.Printf("listening on %s auth=%s ollama=%s", cfg.Addr, cfg.AuthMode(), cfg.OllamaURL)
	log.Fatal(http.ListenAndServe(cfg.Addr, mux))
}

func contextBackground() context.Context { return context.Background() }
