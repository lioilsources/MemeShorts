// Package storyboard převádí klasifikaci memu na seznam scén s vlastními postavami.
package storyboard

import (
	"fmt"
	"math"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/ol1n/memeshorts/internal/classify"
)

// Limity délky výstupu (Shorts/TikTok, plán §0 a QA 3.7).
const (
	MinTotalMs = 6000
	MaxTotalMs = 15000
	minSceneMs = 1500
)

type Character struct {
	Name      string `yaml:"name" json:"name"`
	Archetype string `yaml:"archetype" json:"archetype"`
	Prompt    string `yaml:"prompt" json:"prompt"` // popis pro flux (linie S)
	Tags      string `yaml:"tags" json:"tags"`     // booru tagy pro Animagine (linie B2)
	Seed      int64  `yaml:"seed" json:"seed"`     // seed pixar předlohy; emoce linie S ho sdílejí
	Ref       string `yaml:"ref" json:"ref,omitempty"`
	Color     string `yaml:"color" json:"color"`
}

type CharacterMap struct {
	Style       string                       `yaml:"style"`
	Characters  map[string]Character         `yaml:"characters"`
	DefaultCast map[string]string            `yaml:"default_cast"`
	Templates   map[string]map[string]string `yaml:"templates"`
}

func LoadCharacterMap(path string) (*CharacterMap, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cm CharacterMap
	if err := yaml.Unmarshal(data, &cm); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for role, key := range cm.DefaultCast {
		if _, ok := cm.Characters[key]; !ok {
			return nil, fmt.Errorf("%s: default_cast.%s → neznámá postava %q", path, role, key)
		}
	}
	for tpl, cast := range cm.Templates {
		for role, key := range cast {
			if _, ok := cm.Characters[key]; !ok {
				return nil, fmt.Errorf("%s: templates[%s].%s → neznámá postava %q", path, tpl, role, key)
			}
		}
	}
	return &cm, nil
}

// Cast vrátí klíč postavy pro roli v dané šabloně.
func (cm *CharacterMap) Cast(memeID, role string) (string, error) {
	if key, ok := cm.Templates[memeID][role]; ok {
		return key, nil
	}
	if key, ok := cm.DefaultCast[role]; ok {
		return key, nil
	}
	return "", fmt.Errorf("role %s nemá přiřazenou postavu", role)
}

type Scene struct {
	Index        int       `json:"index"`
	Role         string    `json:"role"`
	CharacterKey string    `json:"character_key"`
	Character    Character `json:"character"`
	PosePrompt   string    `json:"pose_prompt"`
	Emotion      string    `json:"emotion"`
	Text         string    `json:"text"`
	DurationMs   int       `json:"duration_ms"`
	Frames       int       `json:"frames"` // při FPS; směrodatné pro render
	Camera       string    `json:"camera"` // push_in | pull_out | pan_left | pan_right | punch_zoom
	Punchline    bool      `json:"punchline"`
}

type Storyboard struct {
	MemeID      string  `json:"meme_id"`
	Format      string  `json:"format"`
	Scenes      []Scene `json:"scenes"`
	TotalMs     int     `json:"total_ms"`
	TotalFrames int     `json:"total_frames"`
}

func Build(memeID string, cls classify.Result, cm *CharacterMap) (Storyboard, error) {
	sb := Storyboard{MemeID: memeID, Format: cls.Format}
	for i, b := range cls.Beats {
		key, err := cm.Cast(memeID, b.Who)
		if err != nil {
			return sb, err
		}
		ch := cm.Characters[key]
		punch := i == cls.PunchlineIndex
		sb.Scenes = append(sb.Scenes, Scene{
			Index:        i,
			Role:         b.Who,
			CharacterKey: key,
			Character:    ch,
			PosePrompt:   posePrompt(cm.Style, ch, b.Emotion, cls.Format),
			Emotion:      b.Emotion,
			Text:         b.Text,
			DurationMs:   readingMs(b.Text, punch),
			Camera:       camera(cls.Format, i, punch),
			Punchline:    punch,
		})
	}
	fitDuration(&sb)
	return sb, nil
}

func posePrompt(style string, ch Character, emotion, format string) string {
	parts := []string{style, ch.Prompt, emotion + " expression"}
	switch format {
	case "before_after":
		parts = append(parts, "exaggerated gesture, looking at viewer")
	case "dialog":
		parts = append(parts, "talking, three-quarter view")
	case "reaction":
		parts = append(parts, "close-up reaction shot")
	case "text_over":
		parts = append(parts, "sitting at a table, looking at viewer")
	case "panels":
		parts = append(parts, "comic panel composition")
	}
	return strings.Join(parts, ", ")
}

// readingMs: ~14 znaků/s + 0.9 s na zorientování; punchline drží o 0.8 s déle.
func readingMs(text string, punch bool) int {
	ms := 900 + len([]rune(text))*1000/14
	if punch {
		ms += 800
	}
	return max(ms, minSceneMs)
}

func camera(format string, i int, punch bool) string {
	if punch {
		return "punch_zoom"
	}
	switch format {
	case "dialog":
		if i%2 == 0 {
			return "pan_right"
		}
		return "pan_left"
	case "before_after":
		return "pull_out"
	}
	return "push_in"
}

// fitDuration roztáhne/zkrátí scény do 6–15 s se zachováním poměrů.
// Počítá v celých snímcích (30 fps), aby délka videa přesně odpovídala storyboardu.
func fitDuration(sb *Storyboard) {
	total := 0
	for _, s := range sb.Scenes {
		total += s.DurationMs
	}
	target := min(max(total, MinTotalMs), MaxTotalMs)
	k := 1.0
	if total > 0 {
		k = float64(target) / float64(total)
	}
	minFrames, maxFrames := msToFrames(MinTotalMs), msToFrames(MaxTotalMs)
	sceneMin := msToFrames(minSceneMs)
	sum := 0
	for i := range sb.Scenes {
		f := max(int(math.Round(float64(sb.Scenes[i].DurationMs)*k*FPS/1000)), sceneMin)
		sb.Scenes[i].Frames = f
		sum += f
	}
	if n := len(sb.Scenes); n > 0 && sum < minFrames {
		sb.Scenes[n-1].Frames += minFrames - sum
		sum = minFrames
	}
	// Přebytek ubírat z nejdelší scény, dokud se nevejdeme.
	for sum > maxFrames {
		longest := 0
		for i, s := range sb.Scenes {
			if s.Frames > sb.Scenes[longest].Frames {
				longest = i
			}
		}
		if sb.Scenes[longest].Frames <= sceneMin {
			break
		}
		sb.Scenes[longest].Frames--
		sum--
	}
	for i := range sb.Scenes {
		sb.Scenes[i].DurationMs = framesToMs(sb.Scenes[i].Frames)
	}
	sb.TotalFrames = sum
	sb.TotalMs = framesToMs(sum)
}

const FPS = 30

func msToFrames(ms int) int { return int(math.Round(float64(ms) * FPS / 1000)) }
func framesToMs(f int) int  { return int(math.Round(float64(f) * 1000 / FPS)) }
