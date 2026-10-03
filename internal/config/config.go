// Package config načítá nastavení z prostředí (a volitelně z .env v pracovním adresáři).
package config

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Config struct {
	DataDir   string // data/ — SQLite + originály memů (jen pro analýzu)
	JobsDir   string // jobs/<id>/<stage>/
	AssetsDir string // fonty, SFX, hudba, postavy

	LLMBaseURL string // OpenAI-kompatibilní LiteLLM (llm.ol1n.com)
	LLMModel   string
	LLMKey     string
	CFClientID string // Cloudflare Access service token
	CFSecret   string

	GenQueueURL string // gen-queue (…/nim), flux-schnell; okna 07–13 a 19–01
	GenTimeout  time.Duration
	ComfyURL    string // ComfyUI, jen okno 07–13

	RedditEnabled  bool // feature flag, viz plán 3.1
	RedditClientID string
	RedditSecret   string
	RedditUA       string
}

func Load() Config {
	loadDotEnv(".env")
	return Config{
		DataDir:   env("MEMESHORTS_DATA", "data"),
		JobsDir:   env("MEMESHORTS_JOBS", "jobs"),
		AssetsDir: env("MEMESHORTS_ASSETS", "assets"),

		LLMBaseURL: strings.TrimRight(env("LLM_BASE_URL", "https://llm.ol1n.com"), "/"),
		LLMModel:   env("LLM_MODEL", "llm-dev"),
		LLMKey:     os.Getenv("LLM_API_KEY"),
		CFClientID: os.Getenv("CF_ACCESS_CLIENT_ID"),
		CFSecret:   os.Getenv("CF_ACCESS_CLIENT_SECRET"),

		GenQueueURL: strings.TrimRight(env("GEN_QUEUE_URL", "http://192.168.88.66:8091/nim"), "/"),
		GenTimeout:  time.Duration(envInt("GEN_TIMEOUT_SECONDS", 900)) * time.Second,
		ComfyURL:    strings.TrimRight(env("COMFY_URL", "http://192.168.88.66:8188"), "/"),

		RedditEnabled:  os.Getenv("MEMESHORTS_REDDIT") == "1",
		RedditClientID: os.Getenv("REDDIT_CLIENT_ID"),
		RedditSecret:   os.Getenv("REDDIT_CLIENT_SECRET"),
		RedditUA:       env("REDDIT_USER_AGENT", "memeshorts/0.1 (by u/ol1n)"),
	}
}

func (c Config) DBPath() string       { return filepath.Join(c.DataDir, "memes.db") }
func (c Config) OriginalsDir() string { return filepath.Join(c.DataDir, "originals") }

// LLMAvailable — bez CF Access tokenu llm.ol1n.com vrací přihlašovací stránku.
func (c Config) LLMAvailable() bool {
	return c.LLMKey != "" || (c.CFClientID != "" && c.CFSecret != "") || !strings.Contains(c.LLMBaseURL, "ol1n.com")
}

func (c Config) GenAvailable() bool {
	return (c.CFClientID != "" && c.CFSecret != "") || !strings.Contains(c.GenQueueURL, "ol1n.com")
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func envInt(key string, fallback int) int {
	v := os.Getenv(key)
	n := 0
	for _, r := range v {
		if r < '0' || r > '9' {
			return fallback
		}
		n = n*10 + int(r-'0')
	}
	if v == "" {
		return fallback
	}
	return n
}

// loadDotEnv nastaví proměnné z .env, existující prostředí má přednost.
func loadDotEnv(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		if _, set := os.LookupEnv(k); !set {
			os.Setenv(k, v)
		}
	}
}
