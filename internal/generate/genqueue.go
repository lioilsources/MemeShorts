package generate

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net/http"
	"time"

	"github.com/ol1n/memeshorts/internal/caption"
	"github.com/ol1n/memeshorts/internal/storyboard"
)

// GenQueue volá AiStack gen-queue (/nim/flux-schnell): submit → poll → PNG.
// Submit vrací job_id hned, takže obchází 100s timeout Cloudflare edge.
type GenQueue struct {
	BaseURL      string // …/nim
	Model        string // flux-schnell
	CFClientID   string
	CFSecret     string
	HTTP         *http.Client
	PollInterval time.Duration
	Timeout      time.Duration
	Steps        int
}

func NewGenQueue(baseURL, cfID, cfSecret string, timeout time.Duration) *GenQueue {
	return &GenQueue{BaseURL: baseURL, Model: "flux-schnell", CFClientID: cfID, CFSecret: cfSecret,
		HTTP: &http.Client{Timeout: 60 * time.Second}, PollInterval: 2 * time.Second, Timeout: timeout, Steps: 4}
}

func (g *GenQueue) Name() string { return "genqueue:" + g.Model }

func (g *GenQueue) Generate(ctx context.Context, sc storyboard.Scene, out string) error {
	return g.Image(ctx, sc.PosePrompt, W, H, Seed(sc.CharacterKey), out)
}

// Image vygeneruje jeden obrázek (rozměry v násobcích 64, seed < 2³²) a uloží ho jako PNG.
func (g *GenQueue) Image(ctx context.Context, prompt string, width, height int, seed int64, out string) error {
	ctx, cancel := context.WithTimeout(ctx, g.Timeout)
	defer cancel()
	body, _ := json.Marshal(map[string]any{
		"prompt": prompt, "width": width, "height": height, "steps": g.Steps, "seed": seed,
	})
	var sub struct {
		ID string `json:"id"`
	}
	if err := g.do(ctx, http.MethodPost, "/v1/infer", bytes.NewReader(body), &sub); err != nil {
		return fmt.Errorf("gen-queue submit: %w", err)
	}
	if sub.ID == "" {
		return fmt.Errorf("gen-queue submit: chybí id")
	}
	for {
		var st struct {
			Status string `json:"status"`
			Error  string `json:"error"`
		}
		if err := g.do(ctx, http.MethodGet, "/jobs/"+sub.ID, nil, &st); err != nil {
			return fmt.Errorf("gen-queue status %s: %w", sub.ID, err)
		}
		switch st.Status {
		case "done":
			return g.fetch(ctx, sub.ID, out)
		case "error":
			return fmt.Errorf("gen-queue job %s: %s", sub.ID, st.Error)
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("gen-queue job %s: %w", sub.ID, ctx.Err())
		case <-time.After(g.PollInterval):
		}
	}
}

func (g *GenQueue) fetch(ctx context.Context, id, out string) error {
	resp, err := g.request(ctx, http.MethodGet, "/jobs/"+id+"/result", nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	// NIM vrací JPEG (starší verze PNG); zbytek roury čeká PNG.
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("gen-queue result %s: není obrázek (%.80q)", id, data)
	}
	return caption.SavePNG(img, out)
}

func (g *GenQueue) do(ctx context.Context, method, path string, body io.Reader, v any) error {
	resp, err := g.request(ctx, method, path, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return json.NewDecoder(resp.Body).Decode(v)
}

func (g *GenQueue) request(ctx context.Context, method, path string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, method, g.BaseURL+"/"+g.Model+path, body)
	if err != nil {
		return nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if g.CFClientID != "" {
		req.Header.Set("CF-Access-Client-Id", g.CFClientID)
		req.Header.Set("CF-Access-Client-Secret", g.CFSecret)
	}
	resp, err := g.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode >= 300 {
		data, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		resp.Body.Close()
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, data)
	}
	return resp, nil
}
