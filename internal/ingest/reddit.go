package ingest

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/ol1n/memeshorts/internal/db"
)

// DefaultSubreddits — plán 3.1.
var DefaultSubreddits = []string{"memes", "dankmemes", "wholesomememes"}

// Reddit čte /top?t=day přes OAuth (client credentials, read-only). Rate 1 req/s.
type Reddit struct {
	ClientID, Secret, UserAgent string
	AuthURL, APIURL             string
	Client                      *http.Client
	Interval                    time.Duration

	token   string
	expires time.Time
	last    time.Time
}

func NewReddit(clientID, secret, ua string) *Reddit {
	return &Reddit{
		ClientID: clientID, Secret: secret, UserAgent: ua,
		AuthURL:  "https://www.reddit.com/api/v1/access_token",
		APIURL:   "https://oauth.reddit.com",
		Client:   &http.Client{Timeout: 30 * time.Second},
		Interval: time.Second,
	}
}

type redditListing struct {
	Data struct {
		Children []struct {
			Data struct {
				ID        string  `json:"id"`
				Title     string  `json:"title"`
				URL       string  `json:"url"`
				PostHint  string  `json:"post_hint"`
				Over18    bool    `json:"over_18"`
				Score     float64 `json:"score"`
				Subreddit string  `json:"subreddit"`
			} `json:"data"`
		} `json:"children"`
	} `json:"data"`
}

func (r *Reddit) Fetch(ctx context.Context, subs []string, limit int) ([]db.Meme, error) {
	if r.ClientID == "" || r.Secret == "" {
		return nil, fmt.Errorf("reddit: chybí REDDIT_CLIENT_ID / REDDIT_CLIENT_SECRET")
	}
	var out []db.Meme
	now := time.Now()
	for _, sub := range subs {
		var l redditListing
		if err := r.get(ctx, fmt.Sprintf("/r/%s/top?t=day&limit=%d&raw_json=1", url.PathEscape(sub), limit), &l); err != nil {
			return out, fmt.Errorf("reddit r/%s: %w", sub, err)
		}
		for _, c := range l.Data.Children {
			p := c.Data
			if p.Over18 || !isImage(p.URL, p.PostHint) {
				continue
			}
			out = append(out, db.Meme{
				ID:        "reddit:" + p.ID,
				Source:    "reddit",
				Title:     p.Title,
				ImageURL:  p.URL,
				Score:     p.Score,
				FetchedAt: now,
				Rights:    "template-only",
			})
		}
	}
	return out, nil
}

func isImage(u, hint string) bool {
	if hint == "image" {
		return true
	}
	u = strings.ToLower(u)
	for _, ext := range []string{".jpg", ".jpeg", ".png", ".webp"} {
		if strings.HasSuffix(u, ext) {
			return true
		}
	}
	return false
}

func (r *Reddit) get(ctx context.Context, p string, v any) error {
	if err := r.auth(ctx); err != nil {
		return err
	}
	if wait := r.Interval - time.Since(r.last); wait > 0 {
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	r.last = time.Now()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, r.APIURL+p, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+r.token)
	req.Header.Set("User-Agent", r.UserAgent)
	resp, err := r.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

func (r *Reddit) auth(ctx context.Context) error {
	if r.token != "" && time.Now().Before(r.expires) {
		return nil
	}
	form := url.Values{"grant_type": {"client_credentials"}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.AuthURL, strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.SetBasicAuth(r.ClientID, r.Secret)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", r.UserAgent)
	resp, err := r.Client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var t struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
		Error       string `json:"error"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&t); err != nil {
		return fmt.Errorf("reddit auth: %w", err)
	}
	if t.AccessToken == "" {
		return fmt.Errorf("reddit auth: HTTP %d %s", resp.StatusCode, t.Error)
	}
	r.token = t.AccessToken
	r.expires = time.Now().Add(time.Duration(t.ExpiresIn-60) * time.Second)
	return nil
}
