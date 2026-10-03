package generate

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/ol1n/memeshorts/internal/storyboard"
)

// Library bere obrázky ze schválené knihovny assets/characters/<postava>/<linie>/<emoce>.png
// (plán §1: denní roura negeneruje postavy, jen vybírá). Emoce mimo knihovnu → neutral.
type Library struct {
	Dir       string                      // assets/characters
	Line      string                      // pixar | anime
	Canonical func(emotion string) string // mapování emocí ze šablon na knihovní (emotions.yaml)
	Cutout    bool                        // vzít variantu s alfou (_cut.png), pokud existuje
}

func (l Library) Name() string { return "library:" + l.Line }

// Path vrátí cestu k obrázku scény; chybějící emoce spadne na neutral.
func (l Library) Path(sc storyboard.Scene) (string, error) {
	emotion := sc.Emotion
	if l.Canonical != nil {
		emotion = l.Canonical(emotion)
	}
	for _, e := range []string{emotion, "neutral"} {
		base := filepath.Join(l.Dir, sc.CharacterKey, l.Line, e)
		if l.Cutout {
			if _, err := os.Stat(base + "_cut.png"); err == nil {
				return base + "_cut.png", nil
			}
		}
		if _, err := os.Stat(base + ".png"); err == nil {
			return base + ".png", nil
		}
	}
	return "", fmt.Errorf("knihovna: %s/%s nemá %q ani neutral (spusť `memeshorts library approve`)", sc.CharacterKey, l.Line, emotion)
}

func (l Library) Generate(_ context.Context, sc storyboard.Scene, out string) error {
	src, err := l.Path(sc)
	if err != nil {
		return err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(out, data, 0o644)
}

// Available říká, jestli má knihovna pro danou linii aspoň neutral všech postav.
func (l Library) Available(cm *storyboard.CharacterMap) bool {
	for key := range cm.Characters {
		if _, err := os.Stat(filepath.Join(l.Dir, key, l.Line, "neutral.png")); err != nil {
			return false
		}
	}
	return true
}
