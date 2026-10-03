// Package ingest stahuje šablony a trendy memy do SQLite.
// Originální obrázky se ukládají jen lokálně pro analýzu — do výstupu nikdy.
package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/ol1n/memeshorts/internal/db"
)

const imgflipURL = "https://api.imgflip.com/get_memes"

type Imgflip struct {
	URL    string // přepisovatelné v testech
	Client *http.Client
}

func NewImgflip() *Imgflip {
	return &Imgflip{URL: imgflipURL, Client: &http.Client{Timeout: 30 * time.Second}}
}

type imgflipResp struct {
	Success bool `json:"success"`
	Data    struct {
		Memes []struct {
			ID       string `json:"id"`
			Name     string `json:"name"`
			URL      string `json:"url"`
			Width    int    `json:"width"`
			Height   int    `json:"height"`
			BoxCount int    `json:"box_count"`
			Captions int    `json:"captions"`
		} `json:"memes"`
	} `json:"data"`
	ErrorMessage string `json:"error_message"`
}

// Fetch vrátí ~100 populárních šablon. Pořadí v API = popularita → score.
func (f *Imgflip) Fetch(ctx context.Context) ([]db.Meme, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.URL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("imgflip: HTTP %d", resp.StatusCode)
	}
	var r imgflipResp
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, fmt.Errorf("imgflip: %w", err)
	}
	if !r.Success {
		return nil, fmt.Errorf("imgflip: %s", r.ErrorMessage)
	}
	now := time.Now()
	n := len(r.Data.Memes)
	out := make([]db.Meme, 0, n)
	for i, m := range r.Data.Memes {
		out = append(out, db.Meme{
			ID:        "imgflip:" + m.ID,
			Source:    "imgflip",
			Title:     m.Name,
			ImageURL:  m.URL,
			BoxCount:  m.BoxCount,
			Score:     float64(n-i) / float64(n),
			FetchedAt: now,
			Rights:    "template-only",
		})
	}
	return out, nil
}

// Store uloží memy a stáhne chybějící originály do dir. Vrací počet uložených.
func Store(ctx context.Context, d *db.DB, client *http.Client, dir string, memes []db.Meme) (int, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return 0, err
	}
	stored := 0
	for _, m := range memes {
		if m.ImageURL != "" {
			p, err := download(ctx, client, m.ImageURL, filepath.Join(dir, localName(m)))
			if err != nil {
				fmt.Fprintf(os.Stderr, "ingest: %s: obrázek nestažen: %v\n", m.ID, err)
			} else {
				m.LocalPath = p
			}
		}
		if err := d.UpsertMeme(ctx, m); err != nil {
			return stored, fmt.Errorf("%s: %w", m.ID, err)
		}
		stored++
	}
	return stored, nil
}

func localName(m db.Meme) string {
	ext := path.Ext(m.ImageURL)
	if ext == "" || len(ext) > 5 {
		ext = ".jpg"
	}
	return fmt.Sprintf("%s_%s%s", m.Source, m.ID[len(m.Source)+1:], ext)
}

func download(ctx context.Context, client *http.Client, url, dst string) (string, error) {
	if st, err := os.Stat(dst); err == nil && st.Size() > 0 {
		return dst, nil
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	tmp := dst + ".part"
	f, err := os.Create(tmp)
	if err != nil {
		return "", err
	}
	if _, err := io.Copy(f, resp.Body); err != nil {
		f.Close()
		os.Remove(tmp)
		return "", err
	}
	if err := f.Close(); err != nil {
		return "", err
	}
	return dst, os.Rename(tmp, dst)
}
