// Package classify určí formát memu a rozepíše ho na beaty (repliky) pro storyboard.
//
// Šablony (imgflip) texty nemají — LLM napíše vlastní vtip v daném formátu.
// Trendy (reddit) mají texty z OCR; ty LLM jen roztřídí na beaty.
package classify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/ol1n/memeshorts/internal/db"
	"github.com/ol1n/memeshorts/internal/llm"
)

var Formats = []string{"reaction", "dialog", "before_after", "text_over", "panels", "unknown"}

const (
	MaxBeats    = 6
	MaxTextLen  = 120
	maxAttempts = 2
)

type Beat struct {
	Who     string `json:"who"` // A..D — role ve formátu, mapuje se na postavu
	Text    string `json:"text"`
	Emotion string `json:"emotion"`
}

type Result struct {
	Format         string `json:"format"`
	Characters     int    `json:"characters"`
	Beats          []Beat `json:"beats"`
	PunchlineIndex int    `json:"punchline_index"`
	SafeForBrand   bool   `json:"safe_for_brand"`
	NeedsContext   bool   `json:"needs_context"`
}

// ErrSkip — mem je pro kanál nevhodný (plán 3.2).
var ErrSkip = errors.New("classify: skip")

// Skip vrací důvod, proč mem nepoužít, nebo "".
func (r Result) Skip() string {
	switch {
	case !r.SafeForBrand:
		return "safe_for_brand=false"
	case r.NeedsContext:
		return "needs_context=true"
	case r.Format == "unknown":
		return "format=unknown"
	}
	return ""
}

var whoRe = regexp.MustCompile(`^[A-D]$`)

// Validate kontroluje JSON schéma z plánu 3.2.
func (r *Result) Validate() error {
	var errs []string
	if !contains(Formats, r.Format) {
		errs = append(errs, fmt.Sprintf("format %q není z %v", r.Format, Formats))
	}
	if r.Characters < 1 || r.Characters > 4 {
		errs = append(errs, fmt.Sprintf("characters=%d mimo 1..4", r.Characters))
	}
	if len(r.Beats) < 1 || len(r.Beats) > MaxBeats {
		errs = append(errs, fmt.Sprintf("beats: %d položek, povoleno 1..%d", len(r.Beats), MaxBeats))
	}
	whos := map[string]bool{}
	for i, b := range r.Beats {
		if !whoRe.MatchString(b.Who) {
			errs = append(errs, fmt.Sprintf("beats[%d].who=%q musí být A-D", i, b.Who))
		}
		whos[b.Who] = true
		t := strings.TrimSpace(b.Text)
		if t == "" {
			errs = append(errs, fmt.Sprintf("beats[%d].text je prázdný", i))
		}
		if len([]rune(t)) > MaxTextLen {
			errs = append(errs, fmt.Sprintf("beats[%d].text delší než %d znaků", i, MaxTextLen))
		}
		if strings.TrimSpace(b.Emotion) == "" {
			errs = append(errs, fmt.Sprintf("beats[%d].emotion chybí", i))
		}
	}
	if len(whos) > r.Characters && r.Characters >= 1 {
		errs = append(errs, fmt.Sprintf("beats používají %d rolí, characters=%d", len(whos), r.Characters))
	}
	if r.PunchlineIndex < 0 || r.PunchlineIndex >= len(r.Beats) {
		errs = append(errs, fmt.Sprintf("punchline_index=%d mimo rozsah beats", r.PunchlineIndex))
	}
	if len(errs) > 0 {
		return fmt.Errorf("neplatná klasifikace: %s", strings.Join(errs, "; "))
	}
	return nil
}

func Parse(s string) (Result, error) {
	var r Result
	dec := json.NewDecoder(strings.NewReader(llm.ExtractJSON(s)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&r); err != nil {
		return r, fmt.Errorf("JSON: %w", err)
	}
	for i := range r.Beats {
		r.Beats[i].Text = strings.TrimSpace(r.Beats[i].Text)
		r.Beats[i].Emotion = strings.ToLower(strings.TrimSpace(r.Beats[i].Emotion))
	}
	return r, r.Validate()
}

type Classifier struct {
	LLM *llm.Client // nil = offline (jen known templates + ruční texty)
}

// Classify: ruční texty > LLM > offline tabulka známých šablon.
func (c *Classifier) Classify(ctx context.Context, m db.Meme, manual []string) (Result, error) {
	if len(manual) > 0 {
		return FromTexts(m, manual)
	}
	if c.LLM == nil {
		if len(m.Texts) > 0 {
			return FromTexts(m, m.Texts)
		}
		return Result{}, fmt.Errorf("classify %s: LLM není nakonfigurované (CF_ACCESS_CLIENT_ID/SECRET) — zadej texty přes --text", m.ID)
	}
	msgs := []llm.Message{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: userPrompt(m)},
	}
	var lastErr error
	for attempt := 0; attempt < maxAttempts; attempt++ {
		out, err := c.LLM.ChatJSON(ctx, msgs)
		if err != nil {
			return Result{}, err
		}
		r, err := Parse(out)
		if err == nil {
			return r, nil
		}
		lastErr = err
		// Druhý pokus s konkrétní chybou — model ji většinou opraví.
		msgs = append(msgs,
			llm.Message{Role: "assistant", Content: out},
			llm.Message{Role: "user", Content: "Your JSON failed validation: " + err.Error() + ". Return the corrected JSON only."})
	}
	return Result{}, lastErr
}

// FromTexts sestaví klasifikaci z hotových textů (ručních nebo OCR) bez LLM.
// Formát bere z tabulky známých šablon, jinak odhad podle počtu textů.
func FromTexts(m db.Meme, texts []string) (Result, error) {
	var clean []string
	for _, t := range texts {
		if t = strings.TrimSpace(t); t != "" {
			clean = append(clean, t)
		}
	}
	if len(clean) == 0 {
		return Result{}, fmt.Errorf("classify %s: žádné texty", m.ID)
	}
	if len(clean) > MaxBeats {
		clean = clean[:MaxBeats]
	}
	kt, ok := KnownTemplate(m.ID)
	if !ok {
		kt = guessTemplate(len(clean))
	}
	r := Result{Format: kt.Format, SafeForBrand: true, PunchlineIndex: len(clean) - 1}
	whos := map[string]bool{}
	for i, t := range clean {
		role := kt.Roles[min(i, len(kt.Roles)-1)]
		r.Beats = append(r.Beats, Beat{Who: role.Who, Text: t, Emotion: role.Emotion})
		whos[role.Who] = true
	}
	r.Characters = len(whos)
	return r, r.Validate()
}

func contains(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}
