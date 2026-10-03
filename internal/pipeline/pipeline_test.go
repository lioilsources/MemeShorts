package pipeline

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/ol1n/memeshorts/internal/caption"
	"github.com/ol1n/memeshorts/internal/classify"
	"github.com/ol1n/memeshorts/internal/compose"
	"github.com/ol1n/memeshorts/internal/db"
	"github.com/ol1n/memeshorts/internal/generate"
	"github.com/ol1n/memeshorts/internal/storyboard"
)

// End-to-end: ruční texty → placeholder obrázky → kenburns → compose → QA.
// Potřebuje ffmpeg; bez něj se přeskočí.
func TestRunEndToEnd(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg není v PATH")
	}
	if testing.Short() {
		t.Skip("-short")
	}
	ctx := context.Background()
	tmp := t.TempDir()
	d, err := db.Open(filepath.Join(tmp, "memes.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if err := d.UpsertMeme(ctx, db.Meme{ID: "imgflip:181913649", Source: "imgflip", Title: "Drake Hotline Bling", BoxCount: 2, FetchedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	chars, err := storyboard.LoadCharacterMap("../../assets/characters/map.yaml")
	if err != nil {
		t.Fatal(err)
	}
	caps, err := caption.NewRenderer("../../assets/fonts/Anton-Regular.ttf")
	if err != nil {
		t.Fatal(err)
	}
	deps := Deps{
		DB: d, JobsDir: filepath.Join(tmp, "jobs"), OriginalsDir: filepath.Join(tmp, "originals"),
		Classifier: &classify.Classifier{}, Characters: chars,
		Generator: generate.Placeholder{Captions: caps},
		Composer:  &compose.Composer{Captions: caps, SFXDir: filepath.Join(tmp, "sfx")},
	}
	var log []string
	opt := Options{MemeID: "imgflip:181913649", Recipe: "kenburns", Texts: []string{"Writing tests", "Testing in prod"},
		Log: func(f string, a ...any) { log = append(log, f) }}

	res, err := Run(ctx, deps, opt)
	if err != nil {
		t.Fatal(err)
	}
	if !res.QA.Passed {
		t.Fatalf("QA: %v", res.QA.Failures())
	}
	job, err := d.GetJob(ctx, res.JobID)
	if err != nil || job.Status != "ready" {
		t.Fatalf("job: %+v %v", job, err)
	}

	// Druhý běh: vše z cache (manifest), video se nepřepíše.
	st1, _ := os.Stat(res.Output)
	if _, err := Run(ctx, deps, opt); err != nil {
		t.Fatal(err)
	}
	st2, _ := os.Stat(res.Output)
	if !st1.ModTime().Equal(st2.ModTime()) {
		t.Error("opakovaný běh přerenderoval video")
	}

	// Změna textu invaliduje classify a vše za ním.
	opt.Texts = []string{"Reading docs", "Asking in chat"}
	if _, err := Run(ctx, deps, opt); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(filepath.Join(deps.JobsDir, res.JobID, "storyboard", "storyboard.json"))
	var sb storyboard.Storyboard
	json.Unmarshal(raw, &sb)
	if sb.Scenes[0].Text != "Reading docs" {
		t.Errorf("storyboard se nepřepočítal: %q", sb.Scenes[0].Text)
	}
}

func TestComposeRejectsOriginal(t *testing.T) {
	tmp := t.TempDir()
	orig := filepath.Join(tmp, "originals")
	if !isUnder(filepath.Join(orig, "imgflip_1.jpg"), orig) || isUnder(filepath.Join(tmp, "jobs", "x.png"), orig) {
		t.Fatal("isUnder")
	}
	if isUnder(filepath.Join(tmp, "originals2", "x"), orig) {
		t.Fatal("prefix bez separátoru nesmí projít")
	}
}
