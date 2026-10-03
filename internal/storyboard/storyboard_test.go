package storyboard

import (
	"strings"
	"testing"

	"github.com/ol1n/memeshorts/internal/classify"
)

func loadMap(t *testing.T) *CharacterMap {
	t.Helper()
	cm, err := LoadCharacterMap("../../assets/characters/map.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return cm
}

func TestBuildDrake(t *testing.T) {
	cls := classify.Result{Format: "before_after", Characters: 1, PunchlineIndex: 1, SafeForBrand: true,
		Beats: []classify.Beat{{Who: "A", Text: "Writing tests", Emotion: "disgusted"}, {Who: "A", Text: "Testing in production", Emotion: "pleased"}}}
	sb, err := Build("imgflip:181913649", cls, loadMap(t))
	if err != nil {
		t.Fatal(err)
	}
	if len(sb.Scenes) != 2 || sb.Scenes[0].CharacterKey != "kiro" || !sb.Scenes[1].Punchline || sb.Scenes[1].Camera != "punch_zoom" {
		t.Fatalf("%+v", sb)
	}
	if sb.TotalMs < MinTotalMs || sb.TotalMs > MaxTotalMs {
		t.Errorf("total %d mimo limity", sb.TotalMs)
	}
	if !strings.Contains(sb.Scenes[0].PosePrompt, "red zip-up hoodie") || !strings.Contains(sb.Scenes[0].PosePrompt, "disgusted") {
		t.Errorf("pose prompt: %s", sb.Scenes[0].PosePrompt)
	}
}

func TestFitDurationCapsLongStoryboards(t *testing.T) {
	long := strings.Repeat("word ", 22)
	var beats []classify.Beat
	for i := 0; i < 6; i++ {
		beats = append(beats, classify.Beat{Who: "A", Text: long, Emotion: "x"})
	}
	sb, err := Build("reddit:x", classify.Result{Format: "panels", Characters: 1, Beats: beats, PunchlineIndex: 5}, loadMap(t))
	if err != nil {
		t.Fatal(err)
	}
	if sb.TotalMs > MaxTotalMs {
		t.Errorf("total %d > %d", sb.TotalMs, MaxTotalMs)
	}
	sum := 0
	for _, s := range sb.Scenes {
		sum += s.Frames
	}
	if sum != sb.TotalFrames {
		t.Errorf("součet scén %d != total %d snímků", sum, sb.TotalFrames)
	}
}

func TestCastFallsBackToDefault(t *testing.T) {
	cm := loadMap(t)
	if k, _ := cm.Cast("imgflip:112126428", "C"); k != "yuki" {
		t.Errorf("šablona: %s", k)
	}
	if k, _ := cm.Cast("reddit:abc", "B"); k != "mei" {
		t.Errorf("default: %s", k)
	}
}
