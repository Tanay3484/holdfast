// Package ollama is a minimal client for Ollama's chat API with JSON-schema constrained output.
// https://docs.ollama.com/api
package ollama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Message is one chat turn.
type Message struct {
	Role    string `json:"role"` // system | user | assistant
	Content string `json:"content"`
}

// Client talks to one Ollama server and model.
type Client struct {
	BaseURL   string
	Model     string
	NumThread int // 0 lets Ollama choose
	HTTP      *http.Client
}

// New returns a client with a request timeout.
func New(baseURL, model string, timeout time.Duration, numThread int) *Client {
	return &Client{
		BaseURL:   strings.TrimRight(baseURL, "/"),
		Model:     model,
		NumThread: numThread,
		HTTP:      &http.Client{Timeout: timeout},
	}
}

// ChatJSON sends messages, constrains the reply to schema, and decodes it into out.
func (c *Client) ChatJSON(ctx context.Context, messages []Message, schema any, out any) error {
	opts := map[string]any{"temperature": 0.2}
	if c.NumThread > 0 {
		opts["num_thread"] = c.NumThread
	}
	req, err := json.Marshal(map[string]any{
		"model":    c.Model,
		"messages": messages,
		"format":   schema,
		"stream":   false,
		"options":  opts,
	})
	if err != nil {
		return err
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/chat", bytes.NewReader(req))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(httpReq)
	if err != nil {
		return fmt.Errorf("ollama: %w", err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("ollama: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("ollama: status %d: %s", resp.StatusCode, bytes.TrimSpace(raw))
	}
	var env struct {
		Message Message `json:"message"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("ollama: %w", err)
	}
	if strings.TrimSpace(env.Message.Content) == "" {
		return errors.New("ollama: empty reply")
	}
	if err := json.Unmarshal([]byte(env.Message.Content), out); err != nil {
		return fmt.Errorf("ollama: reply is not the requested JSON: %w", err)
	}
	return nil
}
