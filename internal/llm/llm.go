// Package llm je minimální OpenAI-kompatibilní klient pro LiteLLM za Cloudflare Access.
package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	BaseURL    string
	Model      string
	APIKey     string
	CFClientID string
	CFSecret   string
	HTTP       *http.Client
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func New(baseURL, model, key, cfID, cfSecret string) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), Model: model, APIKey: key,
		CFClientID: cfID, CFSecret: cfSecret, HTTP: &http.Client{Timeout: 120 * time.Second}}
}

// ChatJSON pošle konverzaci s response_format=json_object a vrátí obsah odpovědi.
func (c *Client) ChatJSON(ctx context.Context, msgs []Message) (string, error) {
	body, _ := json.Marshal(map[string]any{
		"model":           c.Model,
		"messages":        msgs,
		"temperature":     0.7,
		"response_format": map[string]string{"type": "json_object"},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/v1/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	if c.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.APIKey)
	}
	if c.CFClientID != "" {
		req.Header.Set("CF-Access-Client-Id", c.CFClientID)
		req.Header.Set("CF-Access-Client-Secret", c.CFSecret)
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("llm: HTTP %d: %.200s", resp.StatusCode, data)
	}
	var r struct {
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(data, &r); err != nil {
		return "", fmt.Errorf("llm: neplatná odpověď: %w (%.120s)", err, data)
	}
	if len(r.Choices) == 0 {
		return "", fmt.Errorf("llm: prázdná odpověď")
	}
	return r.Choices[0].Message.Content, nil
}

// ExtractJSON vytáhne první JSON objekt (modely občas přidají ```json nebo <think>).
func ExtractJSON(s string) string {
	if i := strings.LastIndex(s, "</think>"); i >= 0 {
		s = s[i+len("</think>"):]
	}
	start := strings.Index(s, "{")
	end := strings.LastIndex(s, "}")
	if start < 0 || end < start {
		return s
	}
	return s[start : end+1]
}
