package generate

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/jpeg"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ol1n/memeshorts/internal/storyboard"
)

func TestGenQueueSubmitPollFetch(t *testing.T) {
	polls := 0
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("CF-Access-Client-Id") != "id" {
			w.WriteHeader(403)
			return
		}
		switch r.Method + " " + r.URL.Path {
		case "POST /nim/flux-schnell/v1/infer":
			json.NewDecoder(r.Body).Decode(&got)
			w.WriteHeader(202)
			w.Write([]byte(`{"id":"j1","queue_position":0}`))
		case "GET /nim/flux-schnell/jobs/j1":
			polls++
			if polls < 2 {
				w.Write([]byte(`{"status":"running"}`))
			} else {
				w.Write([]byte(`{"status":"done"}`))
			}
		case "GET /nim/flux-schnell/jobs/j1/result":
			jpeg.Encode(w, image.NewRGBA(image.Rect(0, 0, 8, 8)), nil)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	g := NewGenQueue(srv.URL+"/nim", "id", "sec", 5*time.Second)
	g.PollInterval = time.Millisecond
	out := filepath.Join(t.TempDir(), "s.png")
	sc := storyboard.Scene{CharacterKey: "kiro", PosePrompt: "anime boy"}
	if err := g.Generate(context.Background(), sc, out); err != nil {
		t.Fatal(err)
	}
	if got["prompt"] != "anime boy" || got["width"].(float64) != W || int64(got["seed"].(float64)) != Seed("kiro") {
		t.Errorf("payload: %v", got)
	}
	// NIM vrací JPEG, na disk se má dostat PNG.
	if data, _ := os.ReadFile(out); !bytes.HasPrefix(data, []byte("\x89PNG")) {
		t.Errorf("výstup není PNG: %.8q", data)
	}
}

func TestGenQueueJobError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			w.Write([]byte(`{"id":"j1"}`))
			return
		}
		w.Write([]byte(`{"status":"error","error":"NIM 500"}`))
	}))
	defer srv.Close()
	g := NewGenQueue(srv.URL, "", "", time.Second)
	if err := g.Generate(context.Background(), storyboard.Scene{}, filepath.Join(t.TempDir(), "x.png")); err == nil {
		t.Fatal("čekal jsem chybu")
	}
}
