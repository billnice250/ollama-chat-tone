package ollama

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Message struct {
	Role     string `json:"role"`
	Content  string `json:"content"`
	Thinking string `json:"thinking,omitempty"`
}

type ChatRequest struct {
	Model    string    `json:"model"`
	Messages []Message `json:"messages"`
	Stream   bool      `json:"stream"`
	Think    *bool     `json:"think,omitempty"`
}

type ChatResponse struct {
	Message Message `json:"message"`
	Done    bool    `json:"done"`
}

type Model struct {
	Name       string    `json:"name"`
	ModifiedAt time.Time `json:"modified_at"`
	Size       int64     `json:"size"`
}

type Client struct {
	BaseURL    string
	HTTP       *http.Client
	streamHTTP *http.Client
}

func New(base string, timeout time.Duration) *Client {
	return NewWithTLS(base, timeout, nil)
}

// NewWithTLS creates a Client that uses the given TLS configuration (e.g. for
// mutual-TLS). Pass nil to use the default transport.
func NewWithTLS(base string, timeout time.Duration, tlsCfg *tls.Config) *Client {
	var transport http.RoundTripper = http.DefaultTransport
	if tlsCfg != nil {
		transport = &http.Transport{TLSClientConfig: tlsCfg}
	}
	return &Client{
		BaseURL:    base,
		HTTP:       &http.Client{Timeout: timeout, Transport: transport},
		streamHTTP: &http.Client{Transport: transport},
	}
}

func (c *Client) Chat(ctx context.Context, req ChatRequest) (*ChatResponse, error) {
	if req.Model == "" {
		return nil, fmt.Errorf("model is required")
	}
	req.Stream = false
	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	hreq.Header.Set("Content-Type", "application/json")

	resp, err := c.HTTP.Do(hreq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("ollama returned %s: %s", resp.Status, readErrorBody(resp.Body))
	}

	var out ChatResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return &out, nil
}

func (c *Client) StreamChat(ctx context.Context, req ChatRequest, onChunk func(ChatResponse) error) error {
	if req.Model == "" {
		return fmt.Errorf("model is required")
	}
	req.Stream = true
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}

	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/chat", bytes.NewReader(body))
	if err != nil {
		return err
	}
	hreq.Header.Set("Content-Type", "application/json")

	resp, err := c.streamHTTP.Do(hreq)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("ollama returned %s: %s", resp.Status, readErrorBody(resp.Body))
	}

	dec := json.NewDecoder(resp.Body)
	for {
		var out ChatResponse
		if err := dec.Decode(&out); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if err := onChunk(out); err != nil {
			return err
		}
		if out.Done {
			return nil
		}
	}
}

func (c *Client) Models(ctx context.Context) ([]Model, error) {
	hreq, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/api/tags", nil)
	if err != nil {
		return nil, err
	}

	resp, err := c.HTTP.Do(hreq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("ollama returned %s: %s", resp.Status, readErrorBody(resp.Body))
	}

	var out struct {
		Models []Model `json:"models"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	return out.Models, nil
}

func readErrorBody(r io.Reader) string {
	body, err := io.ReadAll(io.LimitReader(r, 4096))
	if err != nil || len(body) == 0 {
		return "empty response body"
	}
	return string(body)
}
