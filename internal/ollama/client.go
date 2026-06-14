package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Stream   bool      `json:"stream"`
}

type ChatResponse struct {
	Message Message `json:"message"`
	Done    bool    `json:"done"`
}

type Client struct {
	BaseURL string
	HTTP    *http.Client
}

func New(base string) *Client {
	return &Client{BaseURL: base, HTTP: &http.Client{Timeout: 5 * time.Minute}}
}

func (c *Client) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	if req.Model == "" { return nil, fmt.Errorf("model is required") }
	req.Stream = false
	body, err := json.Marshal(req)
	if err != nil { return nil, err }

	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/chat", bytes.NewReader(body))
	if err != nil { return nil, err }
	hreq.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(hreq)
	if err != nil { return nil, err }
	defer resp.Body.Close()
	if resp.StatusCode >= 300 { return nil, fmt.Errorf("ollama returned %s", resp.Status) }

	var out ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil { return nil, err }
	return &out, nil
}
