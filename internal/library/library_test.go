package library

import (
	"os"
	"testing"

	"github.com/ol1n/memeshorts/internal/storyboard"
)

func TestEmotionsConfig(t *testing.T) {
	e, err := LoadEmotions("../../assets/characters/emotions.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(e.Keys()) != 12 || e.Keys()[0] != "neutral" {
		t.Fatalf("emoce: %v", e.Keys())
	}
	if e.Canonical("Furious") != "angry" || e.Canonical("smug") != "smug" || e.Canonical("whatever") != "neutral" {
		t.Error("Canonical")
	}
	cm, err := storyboard.LoadCharacterMap("../../assets/characters/map.yaml")
	if err != nil {
		t.Fatal(err)
	}
	for k, ch := range cm.Characters {
		if ch.Prompt == "" || ch.Tags == "" {
			t.Errorf("%s: chybí prompt/tags", k)
		}
	}
	p := e.FluxPrompt(cm.Characters["kiro"], e.Emotions["smug"].Flux[0])
	if !contains(p, "3D animated film still") || !contains(p, "red zip-up hoodie") || !contains(p, "smug") {
		t.Errorf("flux prompt: %s", p)
	}
	tp := e.TagPrompt(cm.Characters["mei"], e.Emotions["shocked"].Tags)
	if !contains(tp, "teal hair") || !contains(tp, "hands on own cheeks") {
		t.Errorf("tag prompt: %s", tp)
	}
}

func TestSelectionRoundTrip(t *testing.T) {
	path := t.TempDir() + "/selection.yaml"
	s := Selection{}
	s.Set("kiro", LineS, "master", "kiro/master_pixar_3.png")
	s.Set("kiro", LineS, "smug", "kiro/pixar_smug_v1_0.png")
	if err := s.Save(path); err != nil {
		t.Fatal(err)
	}
	s2, err := LoadSelection(path)
	if err != nil || s2.Get("kiro", LineS, "smug") != "kiro/pixar_smug_v1_0.png" {
		t.Fatalf("%v %v", s2, err)
	}
}

func TestSetSeed(t *testing.T) {
	path := t.TempDir() + "/map.yaml"
	src := "characters:\n  kiro:\n    name: Kiro\n    seed: 44\n  mei:\n    name: Mei\n    seed: 0\n    ref: \"\"\n"
	if err := writeFile(path, src); err != nil {
		t.Fatal(err)
	}
	if err := setSeed(path, "mei", 33); err != nil {
		t.Fatal(err)
	}
	got, _ := readFile(path)
	if !contains(got, "    seed: 33") || !contains(got, "    seed: 44") {
		t.Errorf("%s", got)
	}
}

func contains(s, sub string) bool {
	return len(sub) == 0 || (len(s) >= len(sub) && indexOf(s, sub) >= 0)
}
func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

func writeFile(p, s string) error { return os.WriteFile(p, []byte(s), 0o644) }
func readFile(p string) (string, error) {
	b, err := os.ReadFile(p)
	return string(b), err
}
