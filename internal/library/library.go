// Package library staví knihovnu postava × emoce (Prompts/01-GRAPHICS_PLAN.md §2, G1).
//
// Dvě linie podle rozhodnutí uživatele po G0:
//   - S:  flux-schnell NIM txt2img, pixar-3d; předloha = prompt + seed, emoce = stejný seed, jiný popis výrazu.
//   - B2: Animagine XL + IP-Adapter Plus z anime předlohy (ComfyUI, jen okno 07–13).
//
// Drafty jdou do drafts/library/<postava>/, výběr uživatele do drafts/library/selection.yaml,
// schválené soubory kopíruje `approve` do assets/characters/<postava>/.
package library

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/ol1n/memeshorts/internal/comfy"
	"github.com/ol1n/memeshorts/internal/generate"
	"github.com/ol1n/memeshorts/internal/spark"
	"github.com/ol1n/memeshorts/internal/storyboard"
)

const (
	W, H          = 832, 1216
	LineS, LineB2 = "pixar", "anime" // názvy linií = podadresáře a klíče ve výběru

	masterSeedsS = 8 // kandidátů předlohy linie S (seedy 11, 22, …)
	masterBatchB = 8 // kandidátů předlohy linie B2 (jedna dávka)
	emotionVarB  = 2 // variant na emoci v linii B2
	ipaWeight    = 0.8
)

// Emotions je obsah assets/characters/emotions.yaml.
type Emotions struct {
	Style struct {
		Pixar        string `yaml:"pixar"`
		Framing      string `yaml:"framing"`
		Neutral      string `yaml:"neutral"`
		TagsQuality  string `yaml:"tags_quality"`
		TagsFraming  string `yaml:"tags_framing"`
		TagsNeutral  string `yaml:"tags_neutral"`
		TagsNegative string `yaml:"tags_negative"`
	} `yaml:"style"`
	Emotions map[string]Emotion `yaml:"emotions"`
	Aliases  map[string]string  `yaml:"aliases"`
	order    []string
}

type Emotion struct {
	Flux   []string `yaml:"flux"`
	Tags   string   `yaml:"tags"`
	Motion string   `yaml:"motion"`
}

func LoadEmotions(path string) (*Emotions, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var e Emotions
	if err := yaml.Unmarshal(data, &e); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	// Pořadí podle souboru (yaml.v3 do mapy pořadí nedá — přečíst znovu jako Node).
	var doc yaml.Node
	yaml.Unmarshal(data, &doc)
	if len(doc.Content) > 0 {
		root := doc.Content[0]
		for i := 0; i+1 < len(root.Content); i += 2 {
			if root.Content[i].Value == "emotions" {
				m := root.Content[i+1]
				for j := 0; j < len(m.Content); j += 2 {
					e.order = append(e.order, m.Content[j].Value)
				}
			}
		}
	}
	for k, v := range e.Emotions {
		if len(v.Flux) == 0 || v.Tags == "" {
			return nil, fmt.Errorf("%s: emoce %s nemá flux/tags", path, k)
		}
	}
	for from, to := range e.Aliases {
		if _, ok := e.Emotions[to]; !ok {
			return nil, fmt.Errorf("%s: alias %s → neznámá emoce %s", path, from, to)
		}
	}
	return &e, nil
}

// Keys vrátí emoce v pořadí ze souboru.
func (e *Emotions) Keys() []string { return append([]string(nil), e.order...) }

// Canonical převede emoci ze šablony/LLM na knihovní; neznámou na neutral.
func (e *Emotions) Canonical(emotion string) string {
	emotion = strings.ToLower(strings.TrimSpace(emotion))
	if _, ok := e.Emotions[emotion]; ok {
		return emotion
	}
	if to, ok := e.Aliases[emotion]; ok {
		return to
	}
	return "neutral"
}

func (e *Emotions) FluxPrompt(ch storyboard.Character, expression string) string {
	return e.Style.Pixar + ". " + fmt.Sprintf(e.Style.Framing, ch.Prompt, expression)
}

func (e *Emotions) TagPrompt(ch storyboard.Character, tags string) string {
	return strings.Join([]string{e.Style.TagsQuality, ch.Tags, tags, e.Style.TagsFraming}, ", ")
}

// Selection je výběr uživatele: postava → linie → "master" nebo emoce → soubor (relativně k drafts/library).
type Selection map[string]map[string]map[string]string

func LoadSelection(path string) (Selection, error) {
	sel := Selection{}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return sel, nil
	}
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(data, &sel); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return sel, nil
}

func (s Selection) Get(char, line, key string) string { return s[char][line][key] }

func (s Selection) Set(char, line, key, file string) {
	if s[char] == nil {
		s[char] = map[string]map[string]string{}
	}
	if s[char][line] == nil {
		s[char][line] = map[string]string{}
	}
	s[char][line][key] = file
}

func (s Selection) Save(path string) error {
	data, err := yaml.Marshal(s)
	if err != nil {
		return err
	}
	head := "# Výběr pro knihovnu postav: postava → linie (pixar = S, anime = B2) → master | <emoce> → soubor v drafts/library.\n# Uprav ručně podle archu (drafts/library/sheet.html) nebo přes `memeshorts library pick`.\n"
	return os.WriteFile(path, append([]byte(head), data...), 0o644)
}

// Cell je jeden běh generátoru (předloha nebo emoce) s měřením a provenance.
type Cell struct {
	ID       string   `json:"id"`
	Char     string   `json:"char"`
	Line     string   `json:"line"`
	Kind     string   `json:"kind"` // master | emotion | cutout
	Emotion  string   `json:"emotion,omitempty"`
	Backend  string   `json:"backend"`
	Model    string   `json:"model"`
	Prompt   string   `json:"prompt"`
	Negative string   `json:"negative,omitempty"`
	Seeds    []int64  `json:"seeds,omitempty"`
	Ref      string   `json:"ref,omitempty"` // referenční obrázek (B2, výřez)
	Files    []string `json:"files"`
	PerImgS  float64  `json:"per_img_s"`
	WallS    float64  `json:"wall_s"`
	Error    string   `json:"error,omitempty"`
	Finished string   `json:"finished,omitempty"`
}

type State struct {
	Cells map[string]*Cell `json:"cells"`
}

type Builder struct {
	Dir        string // drafts/library
	AssetsDir  string // assets
	Workflows  string
	Characters *storyboard.CharacterMap
	Emotions   *Emotions
	NIM        *generate.GenQueue
	Comfy      *comfy.Client
	Now        func() time.Time
	Log        func(format string, args ...any)

	state    State
	sel      Selection
	uploaded map[string]string
	ckpt     string
}

func (b *Builder) init() error {
	if b.Now == nil {
		b.Now = time.Now
	}
	if b.Log == nil {
		b.Log = func(string, ...any) {}
	}
	b.uploaded = map[string]string{}
	if err := os.MkdirAll(b.Dir, 0o755); err != nil {
		return err
	}
	b.state = State{Cells: map[string]*Cell{}}
	if data, err := os.ReadFile(b.statePath()); err == nil {
		json.Unmarshal(data, &b.state)
		if b.state.Cells == nil {
			b.state.Cells = map[string]*Cell{}
		}
	}
	var err error
	b.sel, err = LoadSelection(b.selPath())
	return err
}

func (b *Builder) statePath() string { return filepath.Join(b.Dir, "state.json") }
func (b *Builder) selPath() string   { return filepath.Join(b.Dir, "selection.yaml") }

func (b *Builder) save() error {
	data, _ := json.MarshalIndent(b.state, "", "  ")
	return os.WriteFile(b.statePath(), append(data, '\n'), 0o644)
}

// chars vrátí klíče postav; only (čárkami) omezí výběr.
func (b *Builder) chars(only string) ([]string, error) {
	var out []string
	if only != "" {
		for _, k := range strings.Split(only, ",") {
			k = strings.TrimSpace(k)
			if _, ok := b.Characters.Characters[k]; !ok {
				return nil, fmt.Errorf("neznámá postava %q", k)
			}
			out = append(out, k)
		}
		return out, nil
	}
	for k := range b.Characters.Characters {
		out = append(out, k)
	}
	sort.Strings(out)
	return out, nil
}

func (c *Cell) key() string { return c.Char + "/" + c.ID }

func (b *Builder) done(cell *Cell) bool {
	c, ok := b.state.Cells[cell.key()]
	if !ok || c.Error != "" || len(c.Files) == 0 {
		return false
	}
	for _, f := range c.Files {
		if st, err := os.Stat(filepath.Join(b.Dir, f)); err != nil || st.Size() == 0 {
			return false
		}
	}
	return true
}

func (b *Builder) record(c *Cell, err error) {
	c.Finished = b.Now().Format(time.RFC3339)
	c.Error = ""
	if err != nil {
		c.Error = err.Error()
		b.Log("   ✗ %-32s %v", c.ID, err)
	} else {
		c.PerImgS = c.WallS / float64(max(len(c.Files), 1))
		b.Log("   ✓ %-32s %d soub., %.1f s/ks", c.ID, len(c.Files), c.PerImgS)
	}
	b.state.Cells[c.key()] = c
	b.save()
}

// nimCell vygeneruje jeden obrázek na seed (sériově; fronta gen-queue je sdílená).
func (b *Builder) nimCell(ctx context.Context, c *Cell) error {
	if b.done(c) {
		return nil
	}
	c.Backend, c.Model = "nim", "flux-schnell"
	start := time.Now()
	err := func() error {
		if err := spark.NIMWait(ctx, b.Now(), b.Log); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Join(b.Dir, c.Char), 0o755); err != nil {
			return err
		}
		c.Files = nil
		for i, seed := range c.Seeds {
			rel := filepath.Join(c.Char, fmt.Sprintf("%s_%d.png", c.ID, i))
			if err := b.NIM.Image(ctx, c.Prompt, W, H, seed, filepath.Join(b.Dir, rel)); err != nil {
				return err
			}
			c.Files = append(c.Files, rel)
		}
		return nil
	}()
	c.WallS = time.Since(start).Seconds()
	b.record(c, err)
	return err
}

// comfyCell spustí workflow a stáhne výstupy do adresáře postavy.
func (b *Builder) comfyCell(ctx context.Context, c *Cell, workflow string, params map[string]any, need time.Duration) error {
	if b.done(c) {
		return nil
	}
	c.Backend = "comfyui"
	start := time.Now()
	err := func() error {
		if _, err := spark.ComfyLeft(b.Now(), need); err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Join(b.Dir, c.Char), 0o755); err != nil {
			return err
		}
		wf, err := comfy.LoadWorkflow(filepath.Join(b.Workflows, workflow))
		if err != nil {
			return err
		}
		if err := wf.Set(params); err != nil {
			return err
		}
		res, err := b.Comfy.Run(ctx, wf, need)
		if err != nil {
			return err
		}
		c.Files = nil
		for i, f := range res.Files {
			rel := filepath.Join(c.Char, fmt.Sprintf("%s_%d%s", c.ID, i, filepath.Ext(f.Filename)))
			if err := b.Comfy.Download(ctx, f, filepath.Join(b.Dir, rel)); err != nil {
				return err
			}
			c.Files = append(c.Files, rel)
		}
		return nil
	}()
	c.WallS = time.Since(start).Seconds()
	b.record(c, err)
	return err
}

func (b *Builder) upload(ctx context.Context, rel string) (string, error) {
	if name, ok := b.uploaded[rel]; ok {
		return name, nil
	}
	name, err := b.Comfy.Upload(ctx, filepath.Join(b.Dir, rel), "memeshorts_lib_"+strings.ReplaceAll(rel, string(filepath.Separator), "_"))
	if err == nil {
		b.uploaded[rel] = name
	}
	return name, err
}

func (b *Builder) checkpoint(ctx context.Context) (string, error) {
	if b.ckpt == "" {
		ck, err := b.Comfy.Find(ctx, "CheckpointLoaderSimple", "ckpt_name", "animagine", "hassaku", "noob")
		if err != nil {
			return "", err
		}
		b.ckpt = ck
	}
	return b.ckpt, nil
}

func masterSeeds() []int64 {
	s := make([]int64, masterSeedsS)
	for i := range s {
		s[i] = int64(11 * (i + 1))
	}
	return s
}

// Masters vygeneruje kandidáty předloh obou linií pro dané postavy.
func (b *Builder) Masters(ctx context.Context, only string, lines []string) error {
	if err := b.init(); err != nil {
		return err
	}
	chars, err := b.chars(only)
	if err != nil {
		return err
	}
	for _, key := range chars {
		ch := b.Characters.Characters[key]
		if has(lines, LineS) {
			c := &Cell{ID: "master_pixar", Char: key, Line: LineS, Kind: "master",
				Prompt: b.Emotions.FluxPrompt(ch, b.Emotions.Style.Neutral), Seeds: masterSeeds()}
			b.nimCell(ctx, c)
		}
		if has(lines, LineB2) {
			ckpt, err := b.checkpoint(ctx)
			if err != nil {
				return err
			}
			c := &Cell{ID: "master_anime", Char: key, Line: LineB2, Kind: "master", Model: ckpt,
				Prompt: b.Emotions.TagPrompt(ch, b.Emotions.Style.TagsNeutral), Negative: b.Emotions.Style.TagsNegative, Seeds: []int64{11}}
			b.comfyCell(ctx, c, "sdxl_txt2img.json", map[string]any{
				"ckpt": ckpt, "prompt": c.Prompt, "negative": c.Negative, "seed": c.Seeds[0], "width": W, "height": H, "batch": masterBatchB,
			}, 15*time.Minute)
		}
	}
	return b.Sheet()
}

// masterSeed vrátí seed, kterým vznikla vybraná pixar předloha.
func (b *Builder) masterSeed(char string) (int64, error) {
	file := b.sel.Get(char, LineS, "master")
	c := b.cell(char, "master_pixar")
	if file == "" || c == nil {
		return 0, fmt.Errorf("%s: pixar předloha není vybraná (selection.yaml)", char)
	}
	for i, f := range c.Files {
		if f == file && i < len(c.Seeds) {
			return c.Seeds[i], nil
		}
	}
	return 0, fmt.Errorf("%s: vybraný soubor %s není mezi kandidáty předlohy", char, file)
}

// cell najde buňku podle postavy a ID (buňky jsou v mapě pod "<char>/<id>").
func (b *Builder) cell(char, id string) *Cell { return b.state.Cells[char+"/"+id] }

// Emotions vygeneruje emoce z vybraných předloh.
func (b *Builder) EmotionsRun(ctx context.Context, only string, lines []string, onlyEmotion string) error {
	if err := b.init(); err != nil {
		return err
	}
	chars, err := b.chars(only)
	if err != nil {
		return err
	}
	for _, key := range chars {
		ch := b.Characters.Characters[key]
		if has(lines, LineS) {
			seed, err := b.masterSeed(key)
			if err != nil {
				b.Log("   %v — linie S přeskočena", err)
			} else {
				for _, emo := range b.Emotions.Keys() {
					if onlyEmotion != "" && onlyEmotion != emo {
						continue
					}
					e := b.Emotions.Emotions[emo]
					for v, phrase := range e.Flux {
						c := &Cell{ID: fmt.Sprintf("pixar_%s_v%d", emo, v), Char: key, Line: LineS, Kind: "emotion", Emotion: emo,
							Prompt: b.Emotions.FluxPrompt(ch, phrase), Seeds: []int64{seed}}
						b.nimCell(ctx, c)
					}
				}
			}
		}
		if has(lines, LineB2) {
			master := b.sel.Get(key, LineB2, "master")
			if master == "" {
				b.Log("   %s: anime předloha není vybraná — linie B2 přeskočena", key)
				continue
			}
			ckpt, err := b.checkpoint(ctx)
			if err != nil {
				return err
			}
			img, err := b.upload(ctx, master)
			if err != nil {
				return err
			}
			for _, emo := range b.Emotions.Keys() {
				if onlyEmotion != "" && onlyEmotion != emo {
					continue
				}
				e := b.Emotions.Emotions[emo]
				c := &Cell{ID: "anime_" + emo, Char: key, Line: LineB2, Kind: "emotion", Emotion: emo, Model: ckpt, Ref: master,
					Prompt: b.Emotions.TagPrompt(ch, e.Tags), Negative: b.Emotions.Style.TagsNegative, Seeds: []int64{101}}
				b.comfyCell(ctx, c, "sdxl_ipadapter.json", map[string]any{
					"ckpt": ckpt, "prompt": c.Prompt, "negative": c.Negative, "image": img, "seed": c.Seeds[0],
					"weight": ipaWeight, "width": W, "height": H, "batch": emotionVarB,
				}, 15*time.Minute)
			}
		}
	}
	return b.Sheet()
}

// Cutouts vyřízne pozadí všem vybraným souborům (předlohy i emoce).
func (b *Builder) Cutouts(ctx context.Context, only string) error {
	if err := b.init(); err != nil {
		return err
	}
	chars, err := b.chars(only)
	if err != nil {
		return err
	}
	for _, key := range chars {
		for _, line := range []string{LineS, LineB2} {
			keys := make([]string, 0, len(b.sel[key][line]))
			for k := range b.sel[key][line] {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				src := b.sel[key][line][k]
				img, err := b.upload(ctx, src)
				if err != nil {
					return err
				}
				c := &Cell{ID: fmt.Sprintf("cut_%s_%s", line, k), Char: key, Line: line, Kind: "cutout", Emotion: k, Model: "BiRefNet-general", Ref: src}
				b.comfyCell(ctx, c, "rmbg.json", map[string]any{"image": img, "model": c.Model}, 10*time.Minute)
			}
		}
	}
	return b.Sheet()
}

// SheetOnly jen přegeneruje arch (po ruční úpravě selection.yaml).
func (b *Builder) SheetOnly() error {
	if err := b.init(); err != nil {
		return err
	}
	return b.Sheet()
}

// PickDefaults doplní chybějící výběr emocí první variantou (předběžně, než vybere uživatel).
func (b *Builder) PickDefaults() (int, error) {
	if err := b.init(); err != nil {
		return 0, err
	}
	n := 0
	keys, _ := b.chars("")
	for _, key := range keys {
		for _, line := range []string{LineS, LineB2} {
			for _, emo := range b.Emotions.Keys() {
				if b.sel.Get(key, line, emo) != "" {
					continue
				}
				cells := b.cellsFor(key, line, emo)
				if len(cells) == 0 || cells[0].Error != "" || len(cells[0].Files) == 0 {
					continue
				}
				b.sel.Set(key, line, emo, cells[0].Files[0])
				n++
			}
		}
	}
	if err := b.sel.Save(b.selPath()); err != nil {
		return n, err
	}
	return n, b.Sheet()
}

// Pick zapíše výběr: `pick kiro pixar master kiro/master_pixar_3.png`.
func (b *Builder) Pick(char, line, key, file string) error {
	if err := b.init(); err != nil {
		return err
	}
	if _, ok := b.Characters.Characters[char]; !ok {
		return fmt.Errorf("neznámá postava %q", char)
	}
	if line != LineS && line != LineB2 {
		return fmt.Errorf("linie musí být %s nebo %s", LineS, LineB2)
	}
	if key != "master" {
		if _, ok := b.Emotions.Emotions[key]; !ok {
			return fmt.Errorf("neznámá emoce %q", key)
		}
	}
	file = strings.TrimPrefix(file, b.Dir+"/")
	if _, err := os.Stat(filepath.Join(b.Dir, file)); err != nil {
		return fmt.Errorf("soubor %s v %s není", file, b.Dir)
	}
	b.sel.Set(char, line, key, file)
	if err := b.sel.Save(b.selPath()); err != nil {
		return err
	}
	return b.Sheet()
}

// Approve zkopíruje výběr do assets/characters/<postava>/ s provenance a zapíše seed do map.yaml.
func (b *Builder) Approve(only string) error {
	if err := b.init(); err != nil {
		return err
	}
	chars, err := b.chars(only)
	if err != nil {
		return err
	}
	for _, key := range chars {
		for _, line := range []string{LineS, LineB2} {
			for k, src := range b.sel[key][line] {
				dstDir := filepath.Join(b.AssetsDir, "characters", key, line)
				if err := os.MkdirAll(dstDir, 0o755); err != nil {
					return err
				}
				dst := filepath.Join(dstDir, k+".png")
				if err := copyFile(filepath.Join(b.Dir, src), dst); err != nil {
					return err
				}
				if cut := b.cell(key, fmt.Sprintf("cut_%s_%s", line, k)); cut != nil && len(cut.Files) > 0 && cut.Error == "" {
					if err := copyFile(filepath.Join(b.Dir, cut.Files[0]), filepath.Join(dstDir, k+"_cut.png")); err != nil {
						return err
					}
				}
				prov := b.provenance(key, src)
				data, _ := json.MarshalIndent(prov, "", "  ")
				if err := os.WriteFile(filepath.Join(dstDir, k+".json"), append(data, '\n'), 0o644); err != nil {
					return err
				}
				b.Log("   %s/%s/%s ← %s", key, line, k, src)
			}
		}
		if seed, err := b.masterSeed(key); err == nil {
			if err := setSeed(filepath.Join(b.AssetsDir, "characters", "map.yaml"), key, seed); err != nil {
				return err
			}
		}
	}
	return nil
}

// provenance najde buňku, ze které soubor vznikl (licenční audit, AI Act).
func (b *Builder) provenance(char, file string) map[string]any {
	for _, c := range b.state.Cells {
		if c.Char != char {
			continue
		}
		for i, f := range c.Files {
			if f == file {
				p := map[string]any{"source": file, "backend": c.Backend, "model": c.Model, "prompt": c.Prompt,
					"negative": c.Negative, "generated": c.Finished, "license": license(c)}
				if i < len(c.Seeds) {
					p["seed"] = c.Seeds[i]
				} else if len(c.Seeds) == 1 {
					p["seed"] = c.Seeds[0]
					p["batch_index"] = i
				}
				if c.Ref != "" {
					p["reference"] = c.Ref
				}
				return p
			}
		}
	}
	return map[string]any{"source": file}
}

func license(c *Cell) string {
	switch {
	case c.Model == "flux-schnell":
		return "FLUX.1 [schnell] Apache-2.0"
	case strings.Contains(strings.ToLower(c.Model), "animagine"):
		return "Animagine XL 4.0 — OpenRAIL++ (ověřit komerční podmínky)"
	case strings.Contains(c.Model, "BiRefNet"):
		return "BiRefNet MIT (výřez)"
	}
	return "ověřit"
}

func has(xs []string, s string) bool {
	for _, x := range xs {
		if x == s {
			return true
		}
	}
	return false
}

func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

// setSeed přepíše `seed:` u postavy v map.yaml bez přeformátování zbytku souboru.
func setSeed(path, char string, seed int64) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(data), "\n")
	in := false
	for i, l := range lines {
		t := strings.TrimSpace(l)
		if strings.HasPrefix(l, "  ") && !strings.HasPrefix(l, "    ") && strings.HasSuffix(t, ":") {
			in = t == char+":"
		}
		if in && strings.HasPrefix(t, "seed:") {
			lines[i] = "    seed: " + fmt.Sprint(seed)
			return os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o644)
		}
	}
	return fmt.Errorf("map.yaml: postava %s nemá řádek seed", char)
}
