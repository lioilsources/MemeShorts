package library

import (
	"bytes"
	"html/template"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type sheetVariant struct {
	File   string
	Picked bool
	Cut    string // soubor výřezu, pokud existuje
}

type sheetEmotion struct {
	Key      string
	Variants []sheetVariant
	Time     float64
	Note     string
}

type sheetLine struct {
	Line     string
	Title    string
	Masters  []sheetVariant
	Master   string
	Emotions []sheetEmotion
	Errors   []string
}

type sheetChar struct {
	Key, Name, Color string
	Lines            []sheetLine
}

var lineTitle = map[string]string{
	LineS:  "S — flux-schnell NIM, pixar-3d, seed předlohy",
	LineB2: "B2 — Animagine XL + IP-Adapter Plus, anime",
}

// Sheet vygeneruje drafts/library/sheet.html: pro každou postavu předlohy a emoce obou linií.
func (b *Builder) Sheet() error {
	var chars []sheetChar
	keys, _ := b.chars("")
	for _, key := range keys {
		ch := b.Characters.Characters[key]
		sc := sheetChar{Key: key, Name: ch.Name, Color: ch.Color}
		for _, line := range []string{LineS, LineB2} {
			sl := sheetLine{Line: line, Title: lineTitle[line], Master: b.sel.Get(key, line, "master")}
			prefix := "master_" + line
			if m := b.cell(key, prefix); m != nil {
				if m.Error != "" {
					sl.Errors = append(sl.Errors, m.ID+": "+m.Error)
				}
				for _, f := range m.Files {
					sl.Masters = append(sl.Masters, sheetVariant{File: f, Picked: f == sl.Master, Cut: b.cutFile(key, line, "master")})
				}
			}
			for _, emo := range b.Emotions.Keys() {
				se := sheetEmotion{Key: emo}
				picked := b.sel.Get(key, line, emo)
				ids := b.cellsFor(key, line, emo)
				for _, c := range ids {
					if c.Error != "" {
						sl.Errors = append(sl.Errors, c.ID+": "+c.Error)
						continue
					}
					se.Time += c.PerImgS
					for _, f := range c.Files {
						se.Variants = append(se.Variants, sheetVariant{File: f, Picked: f == picked, Cut: b.cutFile(key, line, emo)})
					}
				}
				if n := len(ids); n > 0 {
					se.Time /= float64(n)
					sl.Emotions = append(sl.Emotions, se)
				}
			}
			if len(sl.Masters) > 0 || len(sl.Emotions) > 0 || len(sl.Errors) > 0 {
				sc.Lines = append(sc.Lines, sl)
			}
		}
		chars = append(chars, sc)
	}
	var buf bytes.Buffer
	if err := libSheetTpl.Execute(&buf, map[string]any{"Chars": chars}); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(b.Dir, "sheet.html"), buf.Bytes(), 0o644)
}

// cellsFor vrátí buňky emoce (linie S jich má víc — jedna na formulaci) v pořadí ID.
func (b *Builder) cellsFor(char, line, emo string) []*Cell {
	var out []*Cell
	for _, c := range b.state.Cells {
		if c.Char == char && c.Line == line && c.Kind == "emotion" && c.Emotion == emo {
			out = append(out, c)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func (b *Builder) cutFile(char, line, key string) string {
	if c := b.cell(char, "cut_"+line+"_"+key); c != nil && c.Error == "" && len(c.Files) > 0 {
		return c.Files[0]
	}
	return ""
}

var libSheetTpl = template.Must(template.New("lib").Funcs(template.FuncMap{
	"base": func(f string) string { return strings.TrimSuffix(filepath.Base(f), ".png") },
}).Parse(`<!doctype html>
<html lang="cs"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1">
<title>MemeShorts — knihovna postav</title>
<style>
:root{--bg:#f6f5f2;--fg:#1c1b19;--mut:#6b675f;--line:#dcd8cf;--card:#fff;--ok:#1b7f4b}
@media (prefers-color-scheme:dark){:root{--bg:#151412;--fg:#ece9e2;--mut:#9a958b;--line:#33302b;--card:#1e1c19;--ok:#5fd08f}}
body{margin:0;padding:24px 16px 64px;background:var(--bg);color:var(--fg);font:15px/1.45 -apple-system,system-ui,sans-serif}
h1{font-size:22px;margin:0 0 4px}h2{font-size:19px;margin:40px 0 6px;display:flex;align-items:center;gap:10px}
h2 i{width:14px;height:14px;border-radius:50%;display:inline-block}
h3{font-size:15px;margin:18px 0 8px}p{color:var(--mut);margin:4px 0 12px;max-width:90ch}
.row{background:var(--card);border:1px solid var(--line);border-radius:10px;padding:12px;margin:0 0 12px}
.grid{display:grid;grid-template-columns:repeat(auto-fill,minmax(130px,1fr));gap:8px}
.emo{display:grid;grid-template-columns:110px 1fr;gap:10px;align-items:start;border-top:1px solid var(--line);padding:8px 0}
.emo:first-child{border-top:0}.emo b{font-size:13px;text-transform:uppercase;letter-spacing:.05em}.emo small{display:block;color:var(--mut)}
figure{margin:0}img{width:100%;display:block;border-radius:6px;background:#8884}
figcaption{font-size:11px;color:var(--mut);margin-top:3px;word-break:break-all}
.pick img{outline:3px solid var(--ok);outline-offset:-3px}.cut img{background:repeating-conic-gradient(#bbb 0 25%,#eee 0 50%) 0 0/16px 16px}
.err{color:#b3261e;font-size:13px}
</style>
<h1>Knihovna postav — výběr</h1>
<p>Zeleně = vybráno (<code>drafts/library/selection.yaml</code>). Výběr: <code>memeshorts library pick &lt;postava&gt; &lt;pixar|anime&gt; &lt;master|emoce&gt; &lt;soubor&gt;</code>. Popisek pod obrázkem je jeho jméno.</p>
{{range .Chars}}<h2 id="{{.Key}}"><i style="background:{{.Color}}"></i>{{.Name}} <small style="color:var(--mut);font-weight:normal">{{.Key}}</small></h2>
{{$ck := .Key}}{{range .Lines}}<div class="row"><h3>{{.Title}}</h3>
{{if .Masters}}<div class="grid">{{range .Masters}}<figure class="{{if .Picked}}pick{{end}}"><a href="{{.File}}"><img loading="lazy" src="{{.File}}"></a><figcaption>{{base .File}}</figcaption></figure>{{end}}</div>{{end}}
{{if .Emotions}}<div style="margin-top:10px">{{range .Emotions}}<div class="emo"><div><b>{{.Key}}</b><small>{{printf "%.0f" .Time}} s/obr.</small></div><div class="grid">{{range .Variants}}<figure class="{{if .Picked}}pick{{end}}"><a href="{{.File}}"><img loading="lazy" src="{{.File}}"></a><figcaption>{{base .File}}</figcaption></figure>{{if and .Picked .Cut}}<figure class="cut"><img loading="lazy" src="{{.Cut}}"><figcaption>výřez</figcaption></figure>{{end}}{{end}}</div></div>{{end}}</div>{{end}}
{{range .Errors}}<div class="err">{{.}}</div>{{end}}
</div>{{end}}{{end}}
</html>
`))
