// Package comfy je klient ComfyUI HTTP API (SPARK :8188, LAN bez auth).
// Používá ho jen stavba knihovny postav a lab — denní roura jede přes gen-queue.
// Na SPARKu se nic nespouští, posílá se jen HTTP (domluva s Directorem).
package comfy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Client struct {
	BaseURL string
	HTTP    *http.Client
	Poll    time.Duration
}

func NewClient(baseURL string) *Client {
	return &Client{BaseURL: strings.TrimRight(baseURL, "/"), HTTP: &http.Client{Timeout: 60 * time.Second}, Poll: 2 * time.Second}
}

// FileRef je výstupní soubor jobu (obrázek i video — SaveWEBM je hlásí taky pod "images").
type FileRef struct {
	Filename  string `json:"filename"`
	Subfolder string `json:"subfolder"`
	Type      string `json:"type"`
}

type Result struct {
	PromptID string
	Files    []FileRef
	Exec     time.Duration // čistý čas běhu podle ComfyUI (bez čekání ve frontě); 0 = neznámý
	Wall     time.Duration // od odeslání po dokončení, včetně fronty
}

// Alive ověří, že ComfyUI odpovídá (mimo okno 07–13 neběží).
func (c *Client) Alive(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err := c.get(ctx, "/system_stats")
	return err
}

// VRAMFreeGiB vrátí volnou paměť prvního zařízení podle /system_stats.
func (c *Client) VRAMFreeGiB(ctx context.Context) (float64, error) {
	raw, err := c.get(ctx, "/system_stats")
	if err != nil {
		return 0, err
	}
	var st struct {
		Devices []struct {
			VRAMFree float64 `json:"vram_free"`
		} `json:"devices"`
	}
	if err := json.Unmarshal(raw, &st); err != nil || len(st.Devices) == 0 {
		return 0, fmt.Errorf("comfy: system_stats bez zařízení")
	}
	return st.Devices[0].VRAMFree / (1 << 30), nil
}

// Free požádá ComfyUI, aby uvolnilo načtené modely (nic se nezastavuje, jen cache).
func (c *Client) Free(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/free",
		strings.NewReader(`{"unload_models":true,"free_memory":true}`))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("comfy: free: %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("comfy: free: HTTP %d", resp.StatusCode)
	}
	time.Sleep(3 * time.Second) // uvolnění proběhne až v další smyčce fronty
	return nil
}

// Run odešle workflow a počká na výsledek.
func (c *Client) Run(ctx context.Context, wf Workflow, timeout time.Duration) (Result, error) {
	start := time.Now()
	id, err := c.submit(ctx, wf)
	if err != nil {
		return Result{}, err
	}
	deadline := start.Add(timeout)
	for {
		res, done, err := c.history(ctx, id)
		if err != nil {
			return Result{PromptID: id}, err
		}
		if done {
			res.Wall = time.Since(start)
			return res, nil
		}
		if time.Now().After(deadline) {
			return Result{PromptID: id}, fmt.Errorf("comfy: job %s nedoběhl do %s", id, timeout)
		}
		select {
		case <-ctx.Done():
			return Result{PromptID: id}, ctx.Err()
		case <-time.After(c.Poll):
		}
	}
}

func (c *Client) submit(ctx context.Context, wf Workflow) (string, error) {
	body, err := json.Marshal(map[string]any{"prompt": wf.Nodes, "client_id": "memeshorts"})
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/prompt", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("comfy: submit: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var sr struct {
		PromptID   string          `json:"prompt_id"`
		Error      json.RawMessage `json:"error"`
		NodeErrors json.RawMessage `json:"node_errors"`
	}
	if err := json.Unmarshal(raw, &sr); err != nil || resp.StatusCode != http.StatusOK || sr.PromptID == "" {
		return "", fmt.Errorf("comfy: workflow odmítnut (HTTP %d): %s", resp.StatusCode, truncate(raw, 900))
	}
	return sr.PromptID, nil
}

func (c *Client) history(ctx context.Context, id string) (Result, bool, error) {
	raw, err := c.get(ctx, "/history/"+url.PathEscape(id))
	if err != nil {
		return Result{}, false, err
	}
	var m map[string]struct {
		Outputs map[string]map[string]json.RawMessage `json:"outputs"`
		Status  struct {
			Completed bool              `json:"completed"`
			StatusStr string            `json:"status_str"`
			Messages  []json.RawMessage `json:"messages"`
		} `json:"status"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		return Result{}, false, fmt.Errorf("comfy: history: %w", err)
	}
	e, ok := m[id]
	if !ok {
		return Result{}, false, nil
	}
	res := Result{PromptID: id}
	var startMs, endMs float64
	var failure string
	for _, msg := range e.Status.Messages {
		var pair []json.RawMessage
		if json.Unmarshal(msg, &pair) != nil || len(pair) != 2 {
			continue
		}
		var kind string
		var data struct {
			Timestamp        float64 `json:"timestamp"`
			NodeType         string  `json:"node_type"`
			ExceptionMessage string  `json:"exception_message"`
		}
		json.Unmarshal(pair[0], &kind)
		json.Unmarshal(pair[1], &data)
		switch kind {
		case "execution_start":
			startMs = data.Timestamp
		case "execution_success":
			endMs = data.Timestamp
		case "execution_error":
			failure = strings.TrimSpace(data.NodeType + ": " + data.ExceptionMessage)
		}
	}
	if !e.Status.Completed || e.Status.StatusStr == "error" {
		return res, true, fmt.Errorf("comfy: job %s selhal (%s) %s", id, e.Status.StatusStr, failure)
	}
	if startMs > 0 && endMs > startMs {
		res.Exec = time.Duration(endMs-startMs) * time.Millisecond
	}
	nodes := make([]string, 0, len(e.Outputs))
	for n := range e.Outputs {
		nodes = append(nodes, n)
	}
	sort.Strings(nodes)
	for _, n := range nodes {
		for _, v := range e.Outputs[n] {
			var files []FileRef
			if json.Unmarshal(v, &files) != nil {
				continue // např. "animated": [true]
			}
			for _, f := range files {
				if f.Filename != "" {
					res.Files = append(res.Files, f)
				}
			}
		}
	}
	if len(res.Files) == 0 {
		return res, true, fmt.Errorf("comfy: job %s doběhl bez výstupů", id)
	}
	return res, true, nil
}

// Download stáhne výstup do dst.
func (c *Client) Download(ctx context.Context, f FileRef, dst string) error {
	q := url.Values{"filename": {f.Filename}, "subfolder": {f.Subfolder}, "type": {f.Type}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/view?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	// Videa jsou velká; výchozí klient má 60s timeout na celé tělo.
	resp, err := (&http.Client{Timeout: 10 * time.Minute}).Do(req)
	if err != nil {
		return fmt.Errorf("comfy: view: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("comfy: view %s: HTTP %d", f.Filename, resp.StatusCode)
	}
	tmp := dst + ".part"
	out, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, resp.Body); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// Upload nahraje obrázek do input/ ComfyUI pod jménem name a vrátí jméno pro LoadImage.
func (c *Client) Upload(ctx context.Context, path, name string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if name == "" {
		name = filepath.Base(path)
	}
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, err := w.CreateFormFile("image", name)
	if err != nil {
		return "", err
	}
	part.Write(data)
	w.WriteField("overwrite", "true")
	w.Close()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/upload/image", &body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("comfy: upload: %w", err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	var ur struct {
		Name      string `json:"name"`
		Subfolder string `json:"subfolder"`
	}
	if resp.StatusCode != http.StatusOK || json.Unmarshal(raw, &ur) != nil || ur.Name == "" {
		return "", fmt.Errorf("comfy: upload %s: HTTP %d: %s", name, resp.StatusCode, truncate(raw, 300))
	}
	if ur.Subfolder != "" {
		return ur.Subfolder + "/" + ur.Name, nil
	}
	return ur.Name, nil
}

// Options vrátí nabídku hodnot combo vstupu nodu (např. seznam checkpointů).
func (c *Client) Options(ctx context.Context, class, input string) ([]string, error) {
	raw, err := c.get(ctx, "/object_info/"+url.PathEscape(class))
	if err != nil {
		return nil, err
	}
	var info map[string]struct {
		Input map[string]map[string][]json.RawMessage `json:"input"`
	}
	if err := json.Unmarshal(raw, &info); err != nil {
		return nil, fmt.Errorf("comfy: object_info %s: %w", class, err)
	}
	node, ok := info[class]
	if !ok {
		return nil, fmt.Errorf("comfy: node %s není nainstalovaný", class)
	}
	for _, group := range node.Input {
		spec, ok := group[input]
		if !ok || len(spec) == 0 {
			continue
		}
		var opts []string
		if json.Unmarshal(spec[0], &opts) == nil {
			return opts, nil
		}
		// Novější zápis: ["COMBO", {"options": [...]}]
		if len(spec) > 1 {
			var meta struct {
				Options []string `json:"options"`
			}
			if json.Unmarshal(spec[1], &meta) == nil && len(meta.Options) > 0 {
				return meta.Options, nil
			}
		}
	}
	return nil, fmt.Errorf("comfy: %s.%s nemá nabídku hodnot", class, input)
}

// Find vrátí první hodnotu z nabídky, která obsahuje některý z podřetězců (v pořadí preferencí).
func (c *Client) Find(ctx context.Context, class, input string, substrings ...string) (string, error) {
	opts, err := c.Options(ctx, class, input)
	if err != nil {
		return "", err
	}
	for _, s := range substrings {
		for _, o := range opts {
			if strings.Contains(strings.ToLower(o), strings.ToLower(s)) {
				return o, nil
			}
		}
	}
	return "", fmt.Errorf("comfy: %s.%s: nic z %v v nabídce (%d položek)", class, input, substrings, len(opts))
}

// HasNode říká, jestli je daný typ nodu nainstalovaný.
func (c *Client) HasNode(ctx context.Context, class string) bool {
	raw, err := c.get(ctx, "/object_info/"+url.PathEscape(class))
	return err == nil && len(bytes.TrimSpace(raw)) > 2
}

func (c *Client) get(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("comfy: %s: %w", path, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("comfy: %s: HTTP %d: %s", path, resp.StatusCode, truncate(raw, 300))
	}
	return raw, nil
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}
