// Package pipeline řídí job: classify → storyboard → generate → animate → compose → qa.
//
// Každý stage zapisuje pod jobs/<id>/<stage>/ a do manifest.json. Klíč stage =
// hash jeho voleb + klíče předchozího stage, takže změna vstupu (texty, recept,
// generátor) přepočítá daný stage i vše za ním; jinak se při opakovaném běhu přeskočí.
package pipeline

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/ol1n/memeshorts/internal/classify"
	"github.com/ol1n/memeshorts/internal/compose"
	"github.com/ol1n/memeshorts/internal/db"
	"github.com/ol1n/memeshorts/internal/generate"
	"github.com/ol1n/memeshorts/internal/qa"
	"github.com/ol1n/memeshorts/internal/recipes"
	"github.com/ol1n/memeshorts/internal/storyboard"
)

var Stages = []string{"classify", "storyboard", "generate", "animate", "compose", "qa"}

type Options struct {
	JobID    string
	MemeID   string
	Recipe   string
	Texts    []string // ruční texty místo LLM
	Force    bool     // ignorovat skip z classify (safe_for_brand / needs_context)
	RedoFrom string   // vynutit přepočet od daného stage
	Log      func(format string, args ...any)
}

type Deps struct {
	DB           *db.DB
	JobsDir      string
	OriginalsDir string
	Classifier   *classify.Classifier
	Characters   *storyboard.CharacterMap
	Generator    generate.Provider
	Composer     *compose.Composer
}

type StageState struct {
	Key        string          `json:"key"`
	Status     string          `json:"status"` // done | failed
	FinishedAt time.Time       `json:"finished_at"`
	Outputs    json.RawMessage `json:"outputs,omitempty"`
	Error      string          `json:"error,omitempty"`
}

type Manifest struct {
	JobID  string                 `json:"job_id"`
	MemeID string                 `json:"meme_id"`
	Recipe string                 `json:"recipe"`
	Stages map[string]*StageState `json:"stages"`
}

type Result struct {
	JobID  string
	Output string
	QA     qa.Report
}

var unsafeID = regexp.MustCompile(`[^a-zA-Z0-9_-]+`)

func DefaultJobID(memeID, recipe string) string {
	return unsafeID.ReplaceAllString(memeID, "-") + "-" + recipe
}

type runner struct {
	Deps
	opt     Options
	dir     string
	man     *Manifest
	prevKey string
	redo    bool
	meme    db.Meme
	cls     classify.Result
	sb      storyboard.Storyboard
	images  []string
	clips   []string
	output  string
}

func Run(ctx context.Context, d Deps, opt Options) (Result, error) {
	if opt.JobID == "" {
		opt.JobID = DefaultJobID(opt.MemeID, opt.Recipe)
	}
	if opt.Log == nil {
		opt.Log = func(string, ...any) {}
	}
	if opt.RedoFrom != "" && indexOf(Stages, opt.RedoFrom) < 0 {
		return Result{}, fmt.Errorf("neznámý stage %q (%s)", opt.RedoFrom, strings.Join(Stages, ", "))
	}
	recipe, err := recipes.Get(opt.Recipe)
	if err != nil {
		return Result{}, err
	}
	meme, err := d.DB.GetMeme(ctx, opt.MemeID)
	if err != nil {
		return Result{}, fmt.Errorf("mem %s: %w (spusť nejdřív `memeshorts ingest`)", opt.MemeID, err)
	}
	r := &runner{Deps: d, opt: opt, dir: filepath.Join(d.JobsDir, opt.JobID), meme: meme}
	if err := r.loadManifest(); err != nil {
		return Result{}, err
	}
	if err := d.DB.UpsertJob(ctx, db.Job{ID: opt.JobID, MemeID: meme.ID, Recipe: opt.Recipe, Status: "running"}); err != nil {
		return Result{}, err
	}

	steps := []struct {
		name string
		opts any
		fn   func(context.Context, string) (any, error)
	}{
		{"classify", map[string]any{"texts": opt.Texts, "force": opt.Force}, r.classify},
		{"storyboard", r.Characters, r.storyboard},
		{"generate", d.Generator.Name(), r.generate},
		{"animate", recipe.Name(), func(ctx context.Context, dir string) (any, error) { return r.animate(ctx, dir, recipe) }},
		{"compose", 2, r.compose}, // číslo = verze kompozice; zvýšit při změně vzhledu
		{"qa", 1, r.qa},
	}
	for _, s := range steps {
		if err := r.stage(ctx, s.name, s.opts, s.fn); err != nil {
			status := "failed"
			if errors.Is(err, classify.ErrSkip) {
				status = "skipped"
			}
			d.DB.UpsertJob(ctx, db.Job{ID: opt.JobID, MemeID: meme.ID, Recipe: opt.Recipe, Status: status})
			return Result{JobID: opt.JobID}, fmt.Errorf("%s: %w", s.name, err)
		}
	}
	var rep qa.Report
	json.Unmarshal(r.man.Stages["qa"].Outputs, &rep)
	status := "ready"
	if !rep.Passed {
		status = "qa_failed"
	}
	outputs, _ := json.Marshal(map[string]any{"video": r.output, "qa": rep})
	if err := d.DB.UpsertJob(ctx, db.Job{ID: opt.JobID, MemeID: meme.ID, Recipe: opt.Recipe, Status: status, Outputs: outputs}); err != nil {
		return Result{}, err
	}
	return Result{JobID: opt.JobID, Output: r.output, QA: rep}, nil
}

// stage spustí fn, pokud stage ještě není hotový se stejným klíčem; jinak načte jeho výstupy.
func (r *runner) stage(ctx context.Context, name string, opts any, fn func(context.Context, string) (any, error)) error {
	optJSON, _ := json.Marshal(opts)
	sum := sha256.Sum256(append([]byte(r.prevKey+"|"+name+"|"), optJSON...))
	key := hex.EncodeToString(sum[:8])
	r.prevKey = key
	if name == r.opt.RedoFrom {
		r.redo = true
	}
	dir := filepath.Join(r.dir, name)
	st := r.man.Stages[name]
	if !r.redo && st != nil && st.Status == "done" && st.Key == key {
		if err := r.restore(name, st.Outputs); err == nil {
			r.opt.Log("  %-10s ✓ (z cache)", name)
			return nil
		}
	}
	r.redo = true // vše za přepočítaným stage se musí přepočítat taky
	os.RemoveAll(dir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	start := time.Now()
	r.opt.Log("  %-10s …", name)
	out, err := fn(ctx, dir)
	st = &StageState{Key: key, FinishedAt: time.Now()}
	if err != nil {
		st.Status, st.Error = "failed", err.Error()
	} else {
		st.Status = "done"
		st.Outputs, _ = json.Marshal(out)
		if rerr := r.restore(name, st.Outputs); rerr != nil {
			st.Status, st.Error, err = "failed", rerr.Error(), rerr
		}
	}
	r.man.Stages[name] = st
	if serr := r.saveManifest(); serr != nil && err == nil {
		err = serr
	}
	if err == nil {
		r.opt.Log("  %-10s ✓ %.1fs", name, time.Since(start).Seconds())
	}
	return err
}

// restore načte výstupy stage do runneru a ověří, že soubory pořád existují.
func (r *runner) restore(name string, raw json.RawMessage) error {
	switch name {
	case "classify":
		if err := json.Unmarshal(raw, &r.cls); err != nil {
			return err
		}
		return r.cls.Validate()
	case "storyboard":
		return json.Unmarshal(raw, &r.sb)
	case "generate":
		if err := json.Unmarshal(raw, &r.images); err != nil {
			return err
		}
		return allExist(r.images)
	case "animate":
		if err := json.Unmarshal(raw, &r.clips); err != nil {
			return err
		}
		return allExist(r.clips)
	case "compose":
		if err := json.Unmarshal(raw, &r.output); err != nil {
			return err
		}
		return allExist([]string{r.output})
	case "qa":
		var rep qa.Report
		return json.Unmarshal(raw, &rep)
	}
	return nil
}

func (r *runner) classify(ctx context.Context, dir string) (any, error) {
	var res classify.Result
	var err error
	// Cache klasifikace u memu; --redo-from classify si vyžádá nový vtip od LLM.
	if len(r.opt.Texts) == 0 && r.meme.Classification != nil && r.opt.RedoFrom != "classify" {
		res, err = classify.Parse(string(r.meme.Classification))
	} else {
		res, err = r.Classifier.Classify(ctx, r.meme, r.opt.Texts)
	}
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(res)
	if len(r.opt.Texts) == 0 {
		// Klasifikace šablony se cachuje u memu; ruční texty jsou jen pro tento job.
		if err := r.DB.SetClassification(ctx, r.meme.ID, res.Format, raw); err != nil {
			return nil, err
		}
	}
	if err := writeJSON(filepath.Join(dir, "classification.json"), res); err != nil {
		return nil, err
	}
	if why := res.Skip(); why != "" && !r.opt.Force {
		return nil, fmt.Errorf("%w: %s (přepiš --force)", classify.ErrSkip, why)
	}
	return res, nil
}

func (r *runner) storyboard(ctx context.Context, dir string) (any, error) {
	sb, err := storyboard.Build(r.meme.ID, r.cls, r.Characters)
	if err != nil {
		return nil, err
	}
	raw, _ := json.Marshal(sb)
	if err := r.DB.UpsertJob(ctx, db.Job{ID: r.opt.JobID, MemeID: r.meme.ID, Recipe: r.opt.Recipe, Status: "running", Storyboard: raw}); err != nil {
		return nil, err
	}
	return sb, writeJSON(filepath.Join(dir, "storyboard.json"), sb)
}

func (r *runner) generate(ctx context.Context, dir string) (any, error) {
	var out []string
	for _, sc := range r.sb.Scenes {
		p := filepath.Join(dir, fmt.Sprintf("scene_%02d.png", sc.Index))
		if err := r.Generator.Generate(ctx, sc, p); err != nil {
			return nil, fmt.Errorf("scéna %d: %w", sc.Index, err)
		}
		out = append(out, p)
	}
	return out, nil
}

func (r *runner) animate(ctx context.Context, dir string, rec recipes.Recipe) (any, error) {
	var out []string
	for i, sc := range r.sb.Scenes {
		p := filepath.Join(dir, fmt.Sprintf("clip_%02d.mp4", sc.Index))
		if err := rec.Animate(ctx, sc, r.images[i], p, i == 0); err != nil {
			return nil, fmt.Errorf("scéna %d: %w", sc.Index, err)
		}
		out = append(out, p)
	}
	return out, nil
}

func (r *runner) compose(ctx context.Context, dir string) (any, error) {
	var in []compose.SceneInput
	for i, sc := range r.sb.Scenes {
		// Pojistka licenční čistoty: originál memu nesmí jít do výstupu (plán §0).
		for _, p := range []string{r.images[i], r.clips[i]} {
			if isUnder(p, r.OriginalsDir) || (r.meme.LocalPath != "" && sameFile(p, r.meme.LocalPath)) {
				return nil, fmt.Errorf("scéna %d používá originál memu (%s) — zakázáno", i, p)
			}
		}
		in = append(in, compose.SceneInput{Scene: sc, Image: r.images[i], Clip: r.clips[i]})
	}
	c := *r.Composer
	c.Title = r.cls.Beats[r.cls.PunchlineIndex].Text
	out := filepath.Join(dir, "short.mp4")
	if err := c.Compose(ctx, in, filepath.Join(dir, "work"), out); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *runner) qa(ctx context.Context, dir string) (any, error) {
	rep, err := qa.Run(ctx, r.output)
	if err != nil {
		return nil, err
	}
	return rep, writeJSON(filepath.Join(dir, "report.json"), rep)
}

func (r *runner) loadManifest() error {
	r.man = &Manifest{JobID: r.opt.JobID, MemeID: r.meme.ID, Recipe: r.opt.Recipe, Stages: map[string]*StageState{}}
	data, err := os.ReadFile(filepath.Join(r.dir, "manifest.json"))
	if errors.Is(err, os.ErrNotExist) {
		return os.MkdirAll(r.dir, 0o755)
	}
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, r.man); err != nil {
		return fmt.Errorf("manifest %s: %w", r.dir, err)
	}
	if r.man.MemeID != r.meme.ID {
		return fmt.Errorf("job %s patří memu %s, ne %s", r.opt.JobID, r.man.MemeID, r.meme.ID)
	}
	if r.man.Stages == nil {
		r.man.Stages = map[string]*StageState{}
	}
	return nil
}

func (r *runner) saveManifest() error {
	return writeJSON(filepath.Join(r.dir, "manifest.json"), r.man)
}

func writeJSON(path string, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func allExist(paths []string) error {
	for _, p := range paths {
		if st, err := os.Stat(p); err != nil || st.Size() == 0 {
			return fmt.Errorf("chybí výstup %s", p)
		}
	}
	return nil
}

func isUnder(p, dir string) bool {
	if dir == "" {
		return false
	}
	ap, err1 := filepath.Abs(p)
	ad, err2 := filepath.Abs(dir)
	if err1 != nil || err2 != nil {
		return false
	}
	rel, err := filepath.Rel(ad, ap)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func sameFile(a, b string) bool {
	sa, err1 := os.Stat(a)
	sb, err2 := os.Stat(b)
	return err1 == nil && err2 == nil && os.SameFile(sa, sb)
}

func indexOf(xs []string, s string) int {
	for i, x := range xs {
		if x == s {
			return i
		}
	}
	return -1
}
