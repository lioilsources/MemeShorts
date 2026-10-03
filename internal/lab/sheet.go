package lab

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// DinoGate — brána identity z labu StoryTelleru (DINOv2 cos k referenci).
// Pozor: měří se celý obrázek, takže změna pózy skóre snižuje i u stejné postavy.
const DinoGate = 0.80

// Dino spočítá podobnost každé emoční varianty k masteru jejího stylu.
// python = interpret s torch (Ol1nLLM tools/lab/.venv), script = tools/lab/dino.py.
func Dino(ctx context.Context, dir, python, script string) error {
	var res Results
	data, err := os.ReadFile(filepath.Join(dir, "results.json"))
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &res); err != nil {
		return err
	}
	byStyle := map[string]map[string]string{}
	for _, c := range res.Cells {
		if c.Group != "emotion" || res.Masters[c.Style] == "" {
			continue
		}
		if byStyle[c.Style] == nil {
			byStyle[c.Style] = map[string]string{}
		}
		for _, f := range c.Files {
			byStyle[c.Style][c.ID+"|"+f] = filepath.Join(dir, f)
		}
	}
	for style, images := range byStyle {
		req, _ := json.Marshal(map[string]any{"ref": filepath.Join(dir, res.Masters[style]), "images": images})
		cmd := exec.CommandContext(ctx, python, script)
		cmd.Stdin = bytes.NewReader(req)
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		out, err := cmd.Output()
		if err != nil {
			return fmt.Errorf("dino (%s): %w: %s", style, err, strings.TrimSpace(stderr.String()))
		}
		var resp struct {
			Cells map[string]struct {
				Dino float64 `json:"dino"`
			} `json:"cells"`
		}
		if err := json.Unmarshal(out, &resp); err != nil {
			return fmt.Errorf("dino (%s): %w", style, err)
		}
		for key, v := range resp.Cells {
			id, file, _ := strings.Cut(key, "|")
			c := res.Cells[id]
			if c.Dino == nil {
				c.Dino = map[string]float64{}
			}
			c.Dino[file] = v.Dino
		}
	}
	out, _ := json.MarshalIndent(res, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "results.json"), append(out, '\n'), 0o644); err != nil {
		return err
	}
	return WriteSheet(dir, res)
}

type sheetRow struct {
	Label, Note, Model string
	PerImg             float64
	DinoAvg            float64
	DinoPass, DinoN    int
	Cells              []*Cell // podle emocí; nil = chybí
}

type sheetData struct {
	Res      Results
	Masters  []*Cell
	Emotions []string
	Rows     []sheetRow
	Cutouts  []*Cell
	Videos   []*Cell
	Errors   []*Cell
	Gate     float64
}

// approachOrder drží řádky archu ve stálém pořadí: nejdřív flux-schnell (primární).
var approachOrder = []string{"S", "C4", "C12", "C12hi", "A", "B1", "B2"}

var approachLabel = map[string]string{
	"S":     "S — flux-schnell NIM, txt2img se seedem masteru",
	"C4":    "C4 — flux-schnell img2img, 4 kroky",
	"C12":   "C12 — flux-schnell img2img, 12 kroků",
	"C12hi": "C12hi — flux-schnell img2img, 12 kroků, vyšší denoise",
	"A":     "A — FLUX Kontext z masteru",
	"B1":    "B1 — anime SDXL img2img",
	"B2":    "B2 — anime SDXL + IP-Adapter Plus",
}

func WriteSheet(dir string, res Results) error {
	d := sheetData{Res: res, Gate: DinoGate}
	for _, e := range g0Emotions {
		d.Emotions = append(d.Emotions, e.Key)
	}
	ids := make([]string, 0, len(res.Cells))
	for id := range res.Cells {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	rows := map[string]*sheetRow{}
	var rowKeys []string
	for _, id := range ids {
		c := res.Cells[id]
		if c.Error != "" {
			d.Errors = append(d.Errors, c)
			continue
		}
		switch c.Group {
		case "master":
			d.Masters = append(d.Masters, c)
		case "cutout":
			d.Cutouts = append(d.Cutouts, c)
		case "video":
			d.Videos = append(d.Videos, c)
		case "emotion":
			key := c.Approach + "|" + c.Style
			r := rows[key]
			if r == nil {
				r = &sheetRow{Label: approachLabel[c.Approach] + " · " + c.Style, Note: c.Note, Model: c.Model, Cells: make([]*Cell, len(d.Emotions))}
				rows[key] = r
				rowKeys = append(rowKeys, key)
			}
			for i, e := range d.Emotions {
				if e == c.Emotion {
					r.Cells[i] = c
				}
			}
		}
	}
	rank := func(key string) int {
		a, _, _ := strings.Cut(key, "|")
		for i, x := range approachOrder {
			if x == a {
				return i
			}
		}
		return len(approachOrder)
	}
	sort.SliceStable(rowKeys, func(i, j int) bool { return rank(rowKeys[i]) < rank(rowKeys[j]) })
	for _, k := range rowKeys {
		r := rows[k]
		var sum float64
		n := 0
		for _, c := range r.Cells {
			if c == nil {
				continue
			}
			sum += c.PerImgS
			n++
			for _, v := range c.Dino {
				r.DinoAvg += v
				r.DinoN++
				if v >= DinoGate {
					r.DinoPass++
				}
			}
		}
		if n > 0 {
			r.PerImg = sum / float64(n)
		}
		if r.DinoN > 0 {
			r.DinoAvg /= float64(r.DinoN)
		}
		d.Rows = append(d.Rows, *r)
	}
	var buf bytes.Buffer
	if err := sheetTpl.Execute(&buf, d); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "sheet.html"), buf.Bytes(), 0o644)
}

var sheetTpl = template.Must(template.New("sheet").Funcs(template.FuncMap{
	"dino": func(c *Cell, f string) string {
		if v, ok := c.Dino[f]; ok {
			return fmt.Sprintf("%.2f", v)
		}
		return ""
	},
	"pass": func(c *Cell, f string) bool { v, ok := c.Dino[f]; return ok && v >= DinoGate },
	"picked": func(res Results, f string) bool {
		for _, m := range res.Masters {
			if m == f {
				return true
			}
		}
		return false
	},
}).Parse(`<!doctype html>
<html lang="cs"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>MemeShorts G0 — {{.Res.Character}}</title>
<style>
:root{--bg:#f6f5f2;--fg:#1c1b19;--mut:#6b675f;--line:#dcd8cf;--card:#fff;--ok:#1b7f4b;--bad:#b3261e}
@media (prefers-color-scheme:dark){:root{--bg:#151412;--fg:#ece9e2;--mut:#9a958b;--line:#33302b;--card:#1e1c19;--ok:#5fd08f;--bad:#ff8a80}}
body{margin:0;padding:24px 16px 64px;background:var(--bg);color:var(--fg);font:15px/1.45 -apple-system,system-ui,sans-serif}
h1{font-size:22px;margin:0 0 4px}h2{font-size:17px;margin:36px 0 10px}p{color:var(--mut);margin:4px 0 12px;max-width:80ch}
.row{background:var(--card);border:1px solid var(--line);border-radius:10px;padding:12px;margin:0 0 14px}
.head{display:flex;flex-wrap:wrap;gap:6px 18px;align-items:baseline;margin-bottom:10px}
.head b{font-size:15px}.head span{color:var(--mut);font-size:13px}
.grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(150px,1fr));gap:8px}
.emo{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:10px}
@media (max-width:760px){.emo{grid-template-columns:repeat(2,minmax(0,1fr))}}
.emo h3{font-size:12px;text-transform:uppercase;letter-spacing:.06em;color:var(--mut);margin:0 0 6px}
.pair{display:grid;grid-template-columns:1fr 1fr;gap:4px}
figure{margin:0;position:relative}img,video{width:100%;display:block;border-radius:6px;background:#8884}
figcaption{font-size:12px;color:var(--mut);margin-top:3px;font-variant-numeric:tabular-nums}
.ok{color:var(--ok)}.bad{color:var(--bad)}.pick{outline:3px solid var(--ok);outline-offset:-3px}
.cut img{background:repeating-conic-gradient(#bbb 0 25%,#eee 0 50%) 0 0/20px 20px}
table{border-collapse:collapse;font-size:13px}td,th{padding:4px 12px 4px 0;text-align:left;border-bottom:1px solid var(--line)}
td.n{text-align:right;font-variant-numeric:tabular-nums}
</style>
<h1>G0 — test stylu a identity ({{.Res.Character}})</h1>
<p>Řádek = postup, sloupec = emoce, v buňce dvě varianty. Číslo pod obrázkem je DINOv2 podobnost k masteru (brána {{.Gate}}); měří celý obrázek, takže změna pózy ji snižuje i u správné postavy — rozhoduje pohled.</p>

<h2>Mastery</h2>
{{range .Masters}}<div class="row"><div class="head"><b>{{.Style}}</b><span>{{.Backend}} · {{.Model}}</span><span>{{printf "%.1f" .PerImgS}} s/obr.</span></div>
<div class="grid">{{$c := .}}{{range .Files}}<figure><a href="{{.}}"><img loading="lazy" src="{{.}}" class="{{if picked $.Res .}}pick{{end}}"></a></figure>{{end}}</div></div>{{end}}
<p>Zeleně orámovaný master je reference pro všechny řádky níže.</p>

<h2>Emoce</h2>
{{range .Rows}}<div class="row"><div class="head"><b>{{.Label}}</b><span>{{.Model}}</span><span>{{.Note}}</span><span>{{printf "%.1f" .PerImg}} s/obr.</span>
{{if .DinoN}}<span>DINO ø {{printf "%.2f" .DinoAvg}}, nad branou {{.DinoPass}}/{{.DinoN}}</span>{{end}}</div>
<div class="emo">{{range $i, $c := .Cells}}<div><h3>{{index $.Emotions $i}}</h3>{{if $c}}<div class="pair">{{range $c.Files}}<figure><a href="{{.}}"><img loading="lazy" src="{{.}}"></a>
<figcaption class="{{if pass $c .}}ok{{else}}bad{{end}}">{{dino $c .}}</figcaption></figure>{{end}}</div>{{else}}<figcaption>—</figcaption>{{end}}</div>{{end}}</div></div>{{end}}

{{if .Cutouts}}<h2>Výřez pozadí</h2><div class="row cut"><div class="grid">{{range .Cutouts}}{{$c := .}}{{range .Files}}<figure><a href="{{.}}"><img loading="lazy" src="{{.}}"></a><figcaption>{{$c.Style}} · {{$c.Model}} · {{printf "%.1f" $c.PerImgS}} s</figcaption></figure>{{end}}{{end}}</div></div>{{end}}

{{if .Videos}}<h2>Pohyb (Wan 2.2 I2V 14B Lightning)</h2><div class="row"><div class="grid">{{range .Videos}}{{$c := .}}{{range .Files}}<figure><video src="{{.}}" controls loop muted playsinline></video><figcaption>{{$c.Emotion}} · {{$c.Note}}<br>{{printf "%.0f" $c.ExecS}} s běh, {{printf "%.0f" $c.WallS}} s celkem</figcaption></figure>{{end}}{{end}}</div></div>{{end}}

{{if .Errors}}<h2>Nedoběhlo</h2><table>{{range .Errors}}<tr><td>{{.ID}}</td><td>{{.Error}}</td></tr>{{end}}</table>{{end}}
</html>
`))
