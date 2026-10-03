// Package lab drží jednorázové experimenty nad SPARKem (Prompts/01-GRAPHICS_PLAN.md).
//
// G0: jedna postava, master + 4 emoce, pět postupů držení identity, výřez pozadí
// a zkušební Wan klipy. Výstup: drafts/g0/ (obrázky, results.json, sheet.html).
// Každá buňka se při opakovaném běhu přeskočí, pokud už má výstupy.
package lab

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/ol1n/memeshorts/internal/comfy"
	"github.com/ol1n/memeshorts/internal/generate"
)

const (
	W, H = 832, 1216 // 9:16-ish, nativní pro SDXL i Wan (hd)
	// Okna SPARKu (AiStack PLAN-spark-scheduler §3). ComfyUI jen 07–13.
	comfyOpen, comfyClose = 7, 13
	videoGuard            = 40 * time.Minute // video job nemá checkpoint
	nimWarmupMin          = 15               // minut po přepnutí profilu, než NIM odpovídá
	// Wan 14B fp8: model 14 GB + umT5 6,7 GB + VAE a aktivace; ComfyUI běží vedle audia a flux NIM.
	videoMinFreeGiB = 30.0
)

// Cell je jedna buňka matice: postup × emoce (nebo master, výřez, klip).
type Cell struct {
	ID       string             `json:"id"`
	Group    string             `json:"group"`    // master | emotion | cutout | video
	Approach string             `json:"approach"` // S, C4, C12, A, B1, B2, …
	Style    string             `json:"style"`    // anime | pixar | sdxl-anime
	Emotion  string             `json:"emotion,omitempty"`
	Backend  string             `json:"backend"`
	Model    string             `json:"model"`
	Prompt   string             `json:"prompt"`
	Seed     int64              `json:"seed"`
	Files    []string           `json:"files"`
	ExecS    float64            `json:"exec_s"`         // čistý běh podle backendu
	WallS    float64            `json:"wall_s"`         // včetně fronty a načtení modelu
	PerImgS  float64            `json:"per_img_s"`      // exec (nebo wall) / počet obrázků
	Dino     map[string]float64 `json:"dino,omitempty"` // soubor → DINOv2 cos k masteru
	Error    string             `json:"error,omitempty"`
	Note     string             `json:"note,omitempty"`
	Finished string             `json:"finished,omitempty"`
}

type Results struct {
	Character string            `json:"character"`
	Masters   map[string]string `json:"masters"` // styl → vybraný master (cesta)
	Cells     map[string]*Cell  `json:"cells"`
}

type G0 struct {
	Dir       string // drafts/g0
	Workflows string // workflows/
	NIM       *generate.GenQueue
	Comfy     *comfy.Client
	Now       func() time.Time
	Log       func(format string, args ...any)

	res      Results
	uploaded map[string]string
}

// --- prompty ---------------------------------------------------------------

// Postava pro test. Popis je záměrně konkrétní (barvy, střih), ať se dá poznat drift.
const (
	kiroFlux = "a young man with messy spiky black hair, sharp dark eyes and a small scar on his left eyebrow, wearing a red zip-up hoodie over a white t-shirt"
	kiroTags = "1boy, solo, male focus, messy hair, spiked hair, black hair, black eyes, scar on eyebrow, red hoodie, open hoodie, white shirt"
)

var fluxStyles = map[string]string{
	"anime": "Anime style illustration, clean bold lineart, flat cel shading, vibrant colors",
	"pixar": "3D animated film still, stylized character with big expressive eyes, soft studio lighting, smooth shading",
}

type emotion struct{ Key, Flux, Tags, Motion string }

var g0Emotions = []emotion{
	{"smug", "a smug self-satisfied smirk, one eyebrow raised, chin lifted, arms crossed over his chest",
		"smug, smirk, raised eyebrow, crossed arms",
		"a smug smirk slowly spreads across his face as he raises one eyebrow and lifts his chin"},
	{"disgusted", "a disgusted grimace, nose wrinkled, mouth twisted, leaning back with one open hand raised in refusal",
		"disgust, grimace, frown, leaning back, hand up",
		"he recoils slightly, wrinkles his nose in disgust and raises one hand in refusal"},
	{"shocked", "a shocked face, eyes wide open, mouth open in a gasp, both hands on his cheeks",
		"surprised, shocked, wide-eyed, open mouth, hands on own cheeks",
		"his eyes go wide and his mouth drops open in shock as he brings both hands to his cheeks"},
	{"exhausted", "an exhausted face, half-closed eyes with dark circles under them, slumped shoulders, head tilted to one side",
		"tired, exhausted, half-closed eyes, bags under eyes, slouching, head tilt",
		"his shoulders slump, his eyelids droop and he lets out a long tired sigh"},
}

const neutralFlux = "a calm neutral expression, arms relaxed at his sides"

func fluxPrompt(style, expression string) string {
	// Bez slov poster/label/book — flux-schnell pak dokresluje falešné popisky.
	return fmt.Sprintf("%s. Waist-up portrait of %s, facing the viewer, with %s. Single character, centered, plain flat light gray studio background.",
		fluxStyles[style], kiroFlux, expression)
}

func kontextPrompt(e emotion) string {
	return fmt.Sprintf("Change the man's expression and pose: he now has %s. Keep exactly the same character, the same hair, face, clothes and colors, the same art style and the same plain gray background.", e.Flux)
}

const (
	tagsQuality  = "masterpiece, best quality, amazing quality, anime coloring, flat color"
	tagsFraming  = "upper body, looking at viewer, simple background, grey background"
	tagsNegative = "lowres, bad anatomy, bad hands, extra digits, worst quality, low quality, jpeg artifacts, blurry, watermark, signature, text, multiple boys, 3d, realistic"
)

func tagPrompt(expr string) string {
	return strings.Join([]string{tagsQuality, kiroTags, expr, tagsFraming}, ", ")
}

const (
	wanTail     = ", anime style, 2D animation, cel shading, clean lineart, flat colors, static camera, one continuous movement, then holds still"
	wanNegative = "blurry, low quality, watermark, text, 3d, realistic, photo, photorealistic, extra limbs, deformed hands, camera movement"

	wanTailPixar     = ", 3D animated film style, smooth shading, soft studio lighting, static camera, one continuous movement, then holds still"
	wanNegativePixar = "blurry, low quality, watermark, text, 2d, flat colors, photo, photorealistic, extra limbs, deformed hands, camera movement"
)

// --- běh -------------------------------------------------------------------

func (g *G0) Run(ctx context.Context, only string) error {
	if g.Now == nil {
		g.Now = time.Now
	}
	if g.Log == nil {
		g.Log = func(string, ...any) {}
	}
	g.uploaded = map[string]string{}
	if err := os.MkdirAll(g.Dir, 0o755); err != nil {
		return err
	}
	g.load()
	g.res.Character = "kiro"

	steps := []struct {
		name string
		fn   func(context.Context) error
	}{
		// SDXL větev na NIM nečeká — NIM po přepnutí profilu v 07:00 pár minut nabíhá.
		{"sdxl", g.sdxlEmotions},
		{"masters", g.masters},
		{"schnell", g.schnellEmotions},
		{"kontext", g.kontextEmotions},
		{"cutout", g.cutouts},
		{"video", g.videos},
	}
	for _, s := range steps {
		if only != "" && only != s.name {
			continue
		}
		g.Log("== %s", s.name)
		if err := s.fn(ctx); err != nil {
			g.Log("!! %s: %v", s.name, err)
			if ctx.Err() != nil {
				return ctx.Err()
			}
		}
	}
	return WriteSheet(g.Dir, g.res)
}

func (g *G0) load() {
	g.res = Results{Masters: map[string]string{}, Cells: map[string]*Cell{}}
	if data, err := os.ReadFile(filepath.Join(g.Dir, "results.json")); err == nil {
		json.Unmarshal(data, &g.res)
	}
	if g.res.Masters == nil {
		g.res.Masters = map[string]string{}
	}
	if g.res.Cells == nil {
		g.res.Cells = map[string]*Cell{}
	}
}

func (g *G0) save() error {
	data, _ := json.MarshalIndent(g.res, "", "  ")
	return os.WriteFile(filepath.Join(g.Dir, "results.json"), append(data, '\n'), 0o644)
}

// done říká, že buňka už doběhla a její soubory existují.
func (g *G0) done(id string) bool {
	c, ok := g.res.Cells[id]
	if !ok || c.Error != "" || len(c.Files) == 0 {
		return false
	}
	for _, f := range c.Files {
		if st, err := os.Stat(filepath.Join(g.Dir, f)); err != nil || st.Size() == 0 {
			return false
		}
	}
	return true
}

func (g *G0) record(c *Cell, err error) {
	c.Finished = g.Now().Format(time.RFC3339)
	c.Error = ""
	if err != nil {
		c.Error = err.Error()
		g.Log("   ✗ %-28s %v", c.ID, err)
	} else {
		n := max(len(c.Files), 1)
		base := c.ExecS
		if base == 0 {
			base = c.WallS
		}
		c.PerImgS = base / float64(n)
		g.Log("   ✓ %-28s %d soub., %.1f s/ks (wall %.0f s)", c.ID, len(c.Files), c.PerImgS, c.WallS)
	}
	g.res.Cells[c.ID] = c
	g.save()
}

// comfyReady hlídá okno: mimo 07–13 se do ComfyUI nic neposílá.
func (g *G0) comfyReady(ctx context.Context, need time.Duration) error {
	now := g.Now()
	closeAt := time.Date(now.Year(), now.Month(), now.Day(), comfyClose, 0, 0, 0, now.Location())
	if now.Hour() < comfyOpen || !now.Before(closeAt) {
		return fmt.Errorf("mimo okno ComfyUI (%02d–%02d), je %s", comfyOpen, comfyClose, now.Format("15:04"))
	}
	if left := closeAt.Sub(now); left < need {
		return fmt.Errorf("do konce okna zbývá %s, potřeba %s", left.Round(time.Minute), need)
	}
	return g.Comfy.Alive(ctx)
}

// comfyCell spustí workflow, stáhne výstupy jako <id>_N.<ext> a zapíše buňku.
func (g *G0) comfyCell(ctx context.Context, c *Cell, workflow string, params map[string]any, timeout time.Duration) error {
	if g.done(c.ID) {
		return nil
	}
	c.Backend = "comfyui"
	err := func() error {
		wf, err := comfy.LoadWorkflow(filepath.Join(g.Workflows, workflow))
		if err != nil {
			return err
		}
		if err := wf.Set(params); err != nil {
			return err
		}
		res, err := g.Comfy.Run(ctx, wf, timeout)
		c.ExecS, c.WallS = res.Exec.Seconds(), res.Wall.Seconds()
		if err != nil {
			return err
		}
		c.Files = nil
		for i, f := range res.Files {
			name := fmt.Sprintf("%s_%d%s", c.ID, i, filepath.Ext(f.Filename))
			if err := g.Comfy.Download(ctx, f, filepath.Join(g.Dir, name)); err != nil {
				return err
			}
			c.Files = append(c.Files, name)
		}
		return nil
	}()
	g.record(c, err)
	return err
}

func (g *G0) upload(ctx context.Context, rel string) (string, error) {
	if name, ok := g.uploaded[rel]; ok {
		return name, nil
	}
	name, err := g.Comfy.Upload(ctx, filepath.Join(g.Dir, rel), "memeshorts_g0_"+filepath.Base(rel))
	if err == nil {
		g.uploaded[rel] = name
	}
	return name, err
}

// nimWait: flux-schnell NIM běží 07–13 a 19–01; po přepnutí profilu v 07:00 a 19:00
// nabíhá, proto se první čtvrthodinu čeká (domluva s Directorem). Mimo okna chyba.
func (g *G0) nimWait(ctx context.Context) error {
	now := g.Now()
	h := now.Hour()
	if !((h >= 7 && h < 13) || h >= 19 || h < 1) {
		return fmt.Errorf("mimo okna flux-schnell (07–13, 19–01), je %s", now.Format("15:04"))
	}
	if (h == 7 || h == 19) && now.Minute() < nimWarmupMin {
		ready := time.Date(now.Year(), now.Month(), now.Day(), h, nimWarmupMin, 0, 0, now.Location())
		g.Log("   NIM nabíhá, čekám do %s", ready.Format("15:04"))
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(ready.Sub(now)):
		}
	}
	return nil
}

// nimCell: flux-schnell NIM txt2img, jeden obrázek na seed.
func (g *G0) nimCell(ctx context.Context, c *Cell, seeds []int64) error {
	if g.done(c.ID) {
		return nil
	}
	c.Backend, c.Model = "nim", "flux-schnell"
	start := time.Now()
	err := func() error {
		if err := g.nimWait(ctx); err != nil {
			return err
		}
		c.Files = nil
		for i, seed := range seeds {
			name := fmt.Sprintf("%s_%d.png", c.ID, i)
			if err := g.NIM.Image(ctx, c.Prompt, W, H, seed, filepath.Join(g.Dir, name)); err != nil {
				return err
			}
			c.Files = append(c.Files, name)
		}
		return nil
	}()
	c.WallS = time.Since(start).Seconds()
	g.record(c, err)
	return err
}

// masters: flux-schnell (anime, pixar) přes NIM — zároveň test obdélníku 832×1216.
func (g *G0) masters(ctx context.Context) error {
	seeds := []int64{11, 22, 33, 44}
	for _, style := range []string{"anime", "pixar"} {
		c := &Cell{ID: "master_" + style, Group: "master", Approach: "txt2img", Style: style,
			Prompt: fluxPrompt(style, neutralFlux), Seed: seeds[0], Note: "4 seedy, NIM 832×1216"}
		if err := g.nimCell(ctx, c, seeds); err != nil {
			continue
		}
		g.pickMaster(style, "master_"+style)
	}
	return nil
}

// masterSDXL: anime-nativní master z SDXL rodiny pro postupy B.
func (g *G0) masterSDXL(ctx context.Context) error {
	ckpt, err := g.Comfy.Find(ctx, "CheckpointLoaderSimple", "ckpt_name", "animagine", "hassaku", "noob")
	if err != nil {
		return err
	}
	c := &Cell{ID: "master_sdxl-anime", Group: "master", Approach: "txt2img", Style: "sdxl-anime", Model: ckpt,
		Prompt: tagPrompt("expressionless, arms at sides"), Seed: 11}
	if err := g.comfyCell(ctx, c, "sdxl_txt2img.json", map[string]any{
		"ckpt": ckpt, "prompt": c.Prompt, "negative": tagsNegative, "seed": c.Seed, "width": W, "height": H, "batch": 4,
	}, 15*time.Minute); err != nil {
		return err
	}
	g.pickMaster("sdxl-anime", c.ID)
	return nil
}

// pickMaster: výchozí master je první varianta; ručně jde přepsat v results.json ("masters").
func (g *G0) pickMaster(style, cellID string) {
	if cur := g.res.Masters[style]; cur != "" {
		if _, err := os.Stat(filepath.Join(g.Dir, cur)); err == nil {
			return
		}
	}
	if c := g.res.Cells[cellID]; c != nil && len(c.Files) > 0 {
		g.res.Masters[style] = c.Files[0]
		g.save()
	}
}

// masterSeed vrátí seed, kterým vznikl vybraný master (pro postup S).
func (g *G0) masterSeed(style string) int64 {
	seeds := []int64{11, 22, 33, 44}
	c := g.res.Cells["master_"+style]
	if c == nil {
		return seeds[0]
	}
	for i, f := range c.Files {
		if f == g.res.Masters[style] && i < len(seeds) {
			return seeds[i]
		}
	}
	return seeds[0]
}

// schnellEmotions: S = NIM txt2img se seedem masteru (jde i večer), C = img2img v ComfyUI.
func (g *G0) schnellEmotions(ctx context.Context) error {
	for _, style := range []string{"anime", "pixar"} {
		master := g.res.Masters[style]
		if master == "" {
			g.Log("   %s: master chybí, přeskakuji", style)
			continue
		}
		seed := g.masterSeed(style)
		for _, e := range g0Emotions {
			c := &Cell{ID: fmt.Sprintf("S_%s_%s", style, e.Key), Group: "emotion", Approach: "S", Style: style, Emotion: e.Key,
				Prompt: fluxPrompt(style, e.Flux), Seed: seed, Note: "txt2img, seed masteru + jiný seed"}
			g.nimCell(ctx, c, []int64{seed, seed + 1000})
		}
	}
	if err := g.comfyReady(ctx, 10*time.Minute); err != nil {
		return err
	}
	variants := []struct {
		approach string
		steps    int
		denoise  float64
	}{{"C4", 4, 0.8}, {"C12", 12, 0.7}, {"C12hi", 12, 0.85}}
	for _, style := range []string{"anime", "pixar"} {
		master := g.res.Masters[style]
		if master == "" {
			continue
		}
		img, err := g.upload(ctx, master)
		if err != nil {
			return err
		}
		for _, v := range variants {
			if style == "pixar" && v.approach != "C12" {
				continue // pixar jen nejnadějnější variantou, ať se okno nezahltí
			}
			for _, e := range g0Emotions {
				c := &Cell{ID: fmt.Sprintf("%s_%s_%s", v.approach, style, e.Key), Group: "emotion", Approach: v.approach, Style: style,
					Emotion: e.Key, Model: "flux1-schnell", Prompt: fluxPrompt(style, e.Flux), Seed: 101,
					Note: fmt.Sprintf("img2img %d kroků, denoise %.2f", v.steps, v.denoise)}
				g.comfyCell(ctx, c, "schnell_img2img.json", map[string]any{
					"prompt": c.Prompt, "image": img, "seed": c.Seed, "steps": v.steps, "denoise": v.denoise, "batch": 2,
				}, 15*time.Minute)
			}
		}
	}
	return nil
}

func (g *G0) kontextEmotions(ctx context.Context) error {
	if err := g.comfyReady(ctx, 20*time.Minute); err != nil {
		return err
	}
	master := g.res.Masters["anime"]
	if master == "" {
		return fmt.Errorf("master anime chybí")
	}
	img, err := g.upload(ctx, master)
	if err != nil {
		return err
	}
	for _, e := range g0Emotions {
		c := &Cell{ID: "A_anime_" + e.Key, Group: "emotion", Approach: "A", Style: "anime", Emotion: e.Key,
			Model: "flux1-dev-kontext", Prompt: kontextPrompt(e), Seed: 101, Note: "Kontext, 20 kroků"}
		g.comfyCell(ctx, c, "kontext_edit.json", map[string]any{
			"prompt": c.Prompt, "image": img, "seed": c.Seed, "steps": 20, "width": W, "height": H, "batch": 2,
		}, 20*time.Minute)
	}
	return nil
}

// sdxlEmotions: B1 = img2img z anime masteru, B2 = IP-Adapter Plus (nová póza z promptu).
func (g *G0) sdxlEmotions(ctx context.Context) error {
	if err := g.comfyReady(ctx, 15*time.Minute); err != nil {
		return err
	}
	if err := g.masterSDXL(ctx); err != nil {
		return err
	}
	master := g.res.Masters["sdxl-anime"]
	mc := g.res.Cells["master_sdxl-anime"]
	if master == "" || mc == nil {
		return fmt.Errorf("master sdxl-anime chybí")
	}
	img, err := g.upload(ctx, master)
	if err != nil {
		return err
	}
	for _, e := range g0Emotions {
		prompt := tagPrompt(e.Tags)
		b1 := &Cell{ID: "B1_sdxl-anime_" + e.Key, Group: "emotion", Approach: "B1", Style: "sdxl-anime", Emotion: e.Key,
			Model: mc.Model, Prompt: prompt, Seed: 101, Note: "img2img, denoise 0.65"}
		g.comfyCell(ctx, b1, "sdxl_img2img.json", map[string]any{
			"ckpt": mc.Model, "prompt": prompt, "negative": tagsNegative, "image": img, "seed": b1.Seed, "denoise": 0.65, "batch": 2,
		}, 15*time.Minute)
		b2 := &Cell{ID: "B2_sdxl-anime_" + e.Key, Group: "emotion", Approach: "B2", Style: "sdxl-anime", Emotion: e.Key,
			Model: mc.Model, Prompt: prompt, Seed: 101, Note: "IP-Adapter Plus 0.8"}
		g.comfyCell(ctx, b2, "sdxl_ipadapter.json", map[string]any{
			"ckpt": mc.Model, "prompt": prompt, "negative": tagsNegative, "image": img, "seed": b2.Seed, "weight": 0.8,
			"width": W, "height": H, "batch": 2,
		}, 15*time.Minute)
	}
	return nil
}

// cutouts: výřez pozadí masterů přes ComfyUI-RMBG (BiRefNet obecný a „toonout").
func (g *G0) cutouts(ctx context.Context) error {
	if _, err := os.Stat(filepath.Join(g.Workflows, "rmbg.json")); err != nil {
		return fmt.Errorf("workflows/rmbg.json zatím není")
	}
	if err := g.comfyReady(ctx, 10*time.Minute); err != nil {
		return err
	}
	styles := make([]string, 0, len(g.res.Masters))
	for s := range g.res.Masters {
		styles = append(styles, s)
	}
	sort.Strings(styles)
	for _, style := range styles {
		img, err := g.upload(ctx, g.res.Masters[style])
		if err != nil {
			return err
		}
		for _, model := range []string{"BiRefNet-general", "BiRefNet_toonout"} {
			c := &Cell{ID: "cut_" + style + "_" + model, Group: "cutout", Approach: "RMBG", Style: style, Model: model, Note: "výřez masteru"}
			g.comfyCell(ctx, c, "rmbg.json", map[string]any{"image": img, "model": model}, 10*time.Minute)
		}
	}
	return nil
}

// videos: zkušební klipy Wan 2.2 I2V 14B Lightning — z neutrálního masteru rovnou do emoce.
func (g *G0) videos(ctx context.Context) error {
	shots := []struct {
		id           string
		style        string
		w, h, length int
		e            emotion
		timeout      time.Duration
	}{
		{"wan_draft49_smug", "anime", 592, 864, 49, g0Emotions[0], 45 * time.Minute},
		{"wan_draft81_shocked", "anime", 592, 864, 81, g0Emotions[2], 45 * time.Minute},
		{"wan_hd49_smug", "anime", W, H, 49, g0Emotions[0], 60 * time.Minute},
		{"wan_draft81_pixar_shocked", "pixar", 592, 864, 81, g0Emotions[2], 45 * time.Minute},
		{"wan_draft49_pixar_smug", "pixar", 592, 864, 49, g0Emotions[0], 45 * time.Minute},
	}
	for _, s := range shots {
		if g.done(s.id) {
			continue
		}
		master := g.res.Masters[s.style]
		if master == "" {
			g.Log("   %s: master %s chybí", s.id, s.style)
			continue
		}
		if err := g.comfyReady(ctx, videoGuard); err != nil {
			return err
		}
		// Před videem pustit vlastní obrázkové modely z paměti a ověřit, že se Wan vejde.
		if err := g.Comfy.Free(ctx); err != nil {
			return err
		}
		// Uvolnění po předchozím klipu se v /system_stats projeví až za desítky sekund.
		var free float64
		for wait := 0; ; wait += 10 {
			var err error
			if free, err = g.Comfy.VRAMFreeGiB(ctx); err != nil {
				return err
			}
			if free >= videoMinFreeGiB || wait >= 120 {
				break
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(10 * time.Second):
			}
		}
		if free < videoMinFreeGiB {
			return fmt.Errorf("volná VRAM %.0f GiB < %.0f GiB, video vynechávám", free, videoMinFreeGiB)
		}
		g.Log("   volná VRAM %.0f GiB", free)
		img, err := g.upload(ctx, master)
		if err != nil {
			return err
		}
		tail, negative := wanTail, wanNegative
		if s.style == "pixar" {
			tail, negative = wanTailPixar, wanNegativePixar
		}
		c := &Cell{ID: s.id, Group: "video", Approach: "Wan14B-L4", Style: s.style, Emotion: s.e.Key,
			Model: "wan2.2_i2v_14B + Lightning 4 kroky", Prompt: "The young man breathes gently, then " + s.e.Motion + tail, Seed: 42,
			Note: fmt.Sprintf("%s, %d×%d, %d snímků @16 fps", s.style, s.w, s.h, s.length)}
		g.comfyCell(ctx, c, "wan_i2v_lightning.json", map[string]any{
			"prompt": c.Prompt, "negative": negative, "image": img, "seed": c.Seed, "width": s.w, "height": s.h, "length": s.length,
		}, s.timeout)
	}
	return nil
}
