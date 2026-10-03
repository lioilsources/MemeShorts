// memeshorts — z meme šablon dělá krátká 9:16 videa s vlastními postavami.
//
//	memeshorts ingest [--source imgflip|reddit|all]
//	memeshorts list [--source imgflip] [--limit 20]
//	memeshorts classify --meme imgflip:181913649 [--text ... --text ...]
//	memeshorts run --meme imgflip:181913649 --recipe kenburns [--text ...] [--gen auto|genqueue|placeholder]
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/ol1n/memeshorts/internal/caption"
	"github.com/ol1n/memeshorts/internal/classify"
	"github.com/ol1n/memeshorts/internal/comfy"
	"github.com/ol1n/memeshorts/internal/compose"
	"github.com/ol1n/memeshorts/internal/config"
	"github.com/ol1n/memeshorts/internal/db"
	"github.com/ol1n/memeshorts/internal/generate"
	"github.com/ol1n/memeshorts/internal/ingest"
	"github.com/ol1n/memeshorts/internal/lab"
	"github.com/ol1n/memeshorts/internal/library"
	"github.com/ol1n/memeshorts/internal/llm"
	"github.com/ol1n/memeshorts/internal/pipeline"
	"github.com/ol1n/memeshorts/internal/recipes"
	"github.com/ol1n/memeshorts/internal/storyboard"
)

const usage = `memeshorts — meme šablony → 9:16 shorts s vlastními postavami

Příkazy:
  ingest    stáhne šablony (Imgflip) / trendy (Reddit, MEMESHORTS_REDDIT=1) do SQLite
  list      vypíše memy v databázi
  classify  klasifikuje mem a vypíše beaty (JSON)
  run       celá roura pro jeden mem → jobs/<id>/compose/short.mp4
  lab       experimenty nad SPARKem (g0: test stylu a identity → drafts/g0/sheet.html)
  library   knihovna postav: masters | emotions | cutout | sheet | pick | approve (drafts/library)

Spusť 'memeshorts <příkaz> -h' pro volby.
`

type multiFlag []string

func (m *multiFlag) String() string     { return strings.Join(*m, " | ") }
func (m *multiFlag) Set(v string) error { *m = append(*m, v); return nil }

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	cfg := config.Load()
	var err error
	switch os.Args[1] {
	case "ingest":
		err = cmdIngest(ctx, cfg, os.Args[2:])
	case "list":
		err = cmdList(ctx, cfg, os.Args[2:])
	case "classify":
		err = cmdClassify(ctx, cfg, os.Args[2:])
	case "run":
		err = cmdRun(ctx, cfg, os.Args[2:])
	case "lab":
		err = cmdLab(ctx, cfg, os.Args[2:])
	case "library":
		err = cmdLibrary(ctx, cfg, os.Args[2:])
	case "-h", "--help", "help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "neznámý příkaz %q\n\n%s", os.Args[1], usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "chyba:", err)
		os.Exit(1)
	}
}

func openDB(cfg config.Config) (*db.DB, error) { return db.Open(cfg.DBPath()) }

func cmdIngest(ctx context.Context, cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("ingest", flag.ExitOnError)
	source := fs.String("source", "imgflip", "imgflip | reddit | all")
	limit := fs.Int("limit", 50, "reddit: počet postů na subreddit")
	fs.Parse(args)
	d, err := openDB(cfg)
	if err != nil {
		return err
	}
	defer d.Close()
	client := &http.Client{Timeout: 30 * time.Second}
	if *source == "imgflip" || *source == "all" {
		memes, err := ingest.NewImgflip().Fetch(ctx)
		if err != nil {
			return err
		}
		n, err := ingest.Store(ctx, d, client, cfg.OriginalsDir(), memes)
		if err != nil {
			return err
		}
		fmt.Printf("imgflip: uloženo %d šablon\n", n)
	}
	if *source == "reddit" || *source == "all" {
		if !cfg.RedditEnabled {
			if *source == "reddit" {
				return errors.New("reddit ingest je za feature flagem — nastav MEMESHORTS_REDDIT=1")
			}
			fmt.Println("reddit: přeskočeno (MEMESHORTS_REDDIT≠1)")
			return nil
		}
		memes, err := ingest.NewReddit(cfg.RedditClientID, cfg.RedditSecret, cfg.RedditUA).Fetch(ctx, ingest.DefaultSubreddits, *limit)
		if err != nil {
			return err
		}
		n, err := ingest.Store(ctx, d, client, cfg.OriginalsDir(), memes)
		if err != nil {
			return err
		}
		fmt.Printf("reddit: uloženo %d memů\n", n)
	}
	return nil
}

func cmdList(ctx context.Context, cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	source := fs.String("source", "", "filtr zdroje")
	limit := fs.Int("limit", 30, "počet")
	fs.Parse(args)
	d, err := openDB(cfg)
	if err != nil {
		return err
	}
	defer d.Close()
	memes, err := d.ListMemes(ctx, *source, *limit)
	if err != nil {
		return err
	}
	for _, m := range memes {
		known := " "
		if _, ok := classify.KnownTemplate(m.ID); ok {
			known = "★"
		}
		fmt.Printf("%s %-20s box=%d %-12s %s\n", known, m.ID, m.BoxCount, m.Format, m.Title)
	}
	if len(memes) == 0 {
		fmt.Println("prázdno — spusť `memeshorts ingest`")
	} else {
		fmt.Println("\n★ = známá šablona (offline klasifikace z --text funguje i bez LLM)")
	}
	return nil
}

func newClassifier(cfg config.Config) *classify.Classifier {
	if !cfg.LLMAvailable() {
		return &classify.Classifier{}
	}
	return &classify.Classifier{LLM: llm.New(cfg.LLMBaseURL, cfg.LLMModel, cfg.LLMKey, cfg.CFClientID, cfg.CFSecret)}
}

// ensureMeme: imgflip šablonu, která v DB chybí, dotáhne automaticky.
func ensureMeme(ctx context.Context, cfg config.Config, d *db.DB, id string) (db.Meme, error) {
	m, err := d.GetMeme(ctx, id)
	if !errors.Is(err, db.ErrNotFound) || !strings.HasPrefix(id, "imgflip:") {
		return m, err
	}
	fmt.Println("mem není v DB, stahuji šablony z Imgflip…")
	memes, err := ingest.NewImgflip().Fetch(ctx)
	if err != nil {
		return m, err
	}
	if _, err := ingest.Store(ctx, d, &http.Client{Timeout: 30 * time.Second}, cfg.OriginalsDir(), memes); err != nil {
		return m, err
	}
	return d.GetMeme(ctx, id)
}

func cmdClassify(ctx context.Context, cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("classify", flag.ExitOnError)
	memeID := fs.String("meme", "", "id memu, např. imgflip:181913649")
	var texts multiFlag
	fs.Var(&texts, "text", "ruční text (opakovatelné) — přeskočí LLM")
	fs.Parse(args)
	if *memeID == "" {
		return errors.New("chybí --meme")
	}
	d, err := openDB(cfg)
	if err != nil {
		return err
	}
	defer d.Close()
	m, err := ensureMeme(ctx, cfg, d, *memeID)
	if err != nil {
		return err
	}
	res, err := newClassifier(cfg).Classify(ctx, m, texts)
	if err != nil {
		return err
	}
	if len(texts) == 0 {
		raw, _ := json.Marshal(res)
		if err := d.SetClassification(ctx, m.ID, res.Format, raw); err != nil {
			return err
		}
	}
	out, _ := json.MarshalIndent(res, "", "  ")
	fmt.Println(string(out))
	if why := res.Skip(); why != "" {
		fmt.Fprintln(os.Stderr, "skip:", why)
	}
	return nil
}

func cmdRun(ctx context.Context, cfg config.Config, args []string) error {
	fs := flag.NewFlagSet("run", flag.ExitOnError)
	memeID := fs.String("meme", "", "id memu, např. imgflip:181913649")
	recipe := fs.String("recipe", "kenburns", "recept: "+strings.Join(recipes.Names(), ", "))
	gen := fs.String("gen", "auto", "generátor obrázků: auto | library | genqueue | placeholder")
	line := fs.String("line", env("MEMESHORTS_LINE", "pixar"), "linie knihovny postav: pixar | anime")
	jobID := fs.String("job", "", "id jobu (default <meme>-<recipe>)")
	redo := fs.String("redo-from", "", "přepočítat od stage: "+strings.Join(pipeline.Stages, ", "))
	force := fs.Bool("force", false, "ignorovat skip z classify (safe_for_brand/needs_context)")
	var texts multiFlag
	fs.Var(&texts, "text", "ruční text beatu (opakovatelné, v pořadí přehrání) — přeskočí LLM")
	fs.Parse(args)
	if *memeID == "" {
		return errors.New("chybí --meme")
	}

	d, err := openDB(cfg)
	if err != nil {
		return err
	}
	defer d.Close()
	if _, err := ensureMeme(ctx, cfg, d, *memeID); err != nil {
		return err
	}
	chars, err := storyboard.LoadCharacterMap(filepath.Join(cfg.AssetsDir, "characters", "map.yaml"))
	if err != nil {
		return err
	}
	caps, err := caption.NewRenderer(filepath.Join(cfg.AssetsDir, "fonts", "Anton-Regular.ttf"))
	if err != nil {
		return err
	}
	emo, err := library.LoadEmotions(filepath.Join(cfg.AssetsDir, "characters", "emotions.yaml"))
	if err != nil {
		return err
	}
	lib := generate.Library{Dir: filepath.Join(cfg.AssetsDir, "characters"), Line: *line, Canonical: emo.Canonical}
	var provider generate.Provider
	switch *gen {
	case "library":
		provider = lib
	case "genqueue":
		provider = generate.NewGenQueue(cfg.GenQueueURL, cfg.CFClientID, cfg.CFSecret, cfg.GenTimeout)
	case "placeholder":
		provider = generate.Placeholder{Captions: caps}
	case "auto":
		// Knihovna je jediný zdroj, který drží postavy (G0); txt2img per scéna by dal pokaždé jinou.
		if lib.Available(chars) {
			provider = lib
		} else {
			fmt.Printf("knihovna postav (%s) není schválená → placeholder obrázky\n", *line)
			provider = generate.Placeholder{Captions: caps}
		}
	default:
		return fmt.Errorf("neznámý generátor %q", *gen)
	}

	fmt.Printf("job %s (%s, %s, %s)\n", firstNonEmpty(*jobID, pipeline.DefaultJobID(*memeID, *recipe)), *memeID, *recipe, provider.Name())
	res, err := pipeline.Run(ctx, pipeline.Deps{
		DB:           d,
		JobsDir:      cfg.JobsDir,
		OriginalsDir: cfg.OriginalsDir(),
		Classifier:   newClassifier(cfg),
		Characters:   chars,
		Generator:    provider,
		Composer: &compose.Composer{
			Captions: caps,
			SFXDir:   filepath.Join(cfg.AssetsDir, "sfx"),
			MusicDir: filepath.Join(cfg.AssetsDir, "music"),
		},
	}, pipeline.Options{
		JobID: *jobID, MemeID: *memeID, Recipe: *recipe, Texts: texts, Force: *force, RedoFrom: *redo,
		Log: func(f string, a ...any) { fmt.Printf(f+"\n", a...) },
	})
	if err != nil {
		return err
	}
	fmt.Printf("\n→ %s (%.1f s)\n", res.Output, res.QA.DurationS)
	if !res.QA.Passed {
		return fmt.Errorf("QA neprošlo: %s", strings.Join(res.QA.Failures(), "; "))
	}
	fmt.Println("QA ✓")
	return nil
}

// cmdLab: `lab g0 [--only krok]` spustí test, `lab dino` doplní metriku identity do archu.
func cmdLab(ctx context.Context, cfg config.Config, args []string) error {
	if len(args) == 0 {
		return errors.New("použití: memeshorts lab g0 [--only sdxl|masters|schnell|kontext|cutout|video] | lab dino")
	}
	fs := flag.NewFlagSet("lab", flag.ExitOnError)
	only := fs.String("only", "", "spustit jen jeden krok")
	dir := fs.String("dir", filepath.Join("drafts", "g0"), "výstupní adresář")
	python := fs.String("python", os.Getenv("LAB_DINO_PYTHON"), "dino: interpret s torch")
	script := fs.String("script", os.Getenv("LAB_DINO_SCRIPT"), "dino: cesta k dino.py")
	fs.Parse(args[1:])
	switch args[0] {
	case "g0":
		g := &lab.G0{
			Dir:       *dir,
			Workflows: "workflows",
			// Fronta gen-queue je sdílená s dávkami jiných projektů — čekat i desítky minut.
			NIM:   generate.NewGenQueue(cfg.GenQueueURL, cfg.CFClientID, cfg.CFSecret, 45*time.Minute),
			Comfy: comfy.NewClient(cfg.ComfyURL),
			Log:   func(f string, a ...any) { fmt.Printf(time.Now().Format("15:04:05 ")+f+"\n", a...) },
		}
		if err := g.Run(ctx, *only); err != nil {
			return err
		}
		fmt.Println("→", filepath.Join(*dir, "sheet.html"))
		return nil
	case "dino":
		if *python == "" || *script == "" {
			return errors.New("dino: chybí --python a --script (nebo LAB_DINO_PYTHON, LAB_DINO_SCRIPT)")
		}
		return lab.Dino(ctx, *dir, *python, *script)
	}
	return fmt.Errorf("neznámý lab příkaz %q", args[0])
}

// cmdLibrary staví knihovnu postava × emoce (G1).
func cmdLibrary(ctx context.Context, cfg config.Config, args []string) error {
	use := "použití: memeshorts library masters|emotions|cutout|sheet|pick-defaults|approve [--chars kiro,mei] [--lines pixar,anime] [--emotion smug]\n" +
		"         memeshorts library pick <postava> <pixar|anime> <master|emoce> <soubor>"
	if len(args) == 0 {
		return errors.New(use)
	}
	fs := flag.NewFlagSet("library", flag.ExitOnError)
	chars := fs.String("chars", "", "jen tyto postavy (čárkami)")
	lines := fs.String("lines", "pixar,anime", "linie: pixar (S), anime (B2)")
	emotion := fs.String("emotion", "", "jen tato emoce")
	rest := args[1:]
	if args[0] != "pick" {
		fs.Parse(rest)
	}
	chm, err := storyboard.LoadCharacterMap(filepath.Join(cfg.AssetsDir, "characters", "map.yaml"))
	if err != nil {
		return err
	}
	emo, err := library.LoadEmotions(filepath.Join(cfg.AssetsDir, "characters", "emotions.yaml"))
	if err != nil {
		return err
	}
	b := &library.Builder{
		Dir: filepath.Join("drafts", "library"), AssetsDir: cfg.AssetsDir, Workflows: "workflows",
		Characters: chm, Emotions: emo,
		NIM:   generate.NewGenQueue(cfg.GenQueueURL, cfg.CFClientID, cfg.CFSecret, 45*time.Minute),
		Comfy: comfy.NewClient(cfg.ComfyURL),
		Log:   func(f string, a ...any) { fmt.Printf(time.Now().Format("15:04:05 ")+f+"\n", a...) },
	}
	ln := strings.Split(*lines, ",")
	switch args[0] {
	case "masters":
		err = b.Masters(ctx, *chars, ln)
	case "emotions":
		err = b.EmotionsRun(ctx, *chars, ln, *emotion)
	case "cutout":
		err = b.Cutouts(ctx, *chars)
	case "sheet":
		err = b.SheetOnly()
	case "pick-defaults":
		var n int
		n, err = b.PickDefaults()
		fmt.Printf("doplněno %d emocí první variantou\n", n)
	case "approve":
		err = b.Approve(*chars)
	case "pick":
		if len(rest) != 4 {
			return errors.New(use)
		}
		err = b.Pick(rest[0], rest[1], rest[2], rest[3])
	default:
		return fmt.Errorf("neznámý library příkaz %q\n%s", args[0], use)
	}
	if err != nil {
		return err
	}
	fmt.Println("→ drafts/library/sheet.html")
	return nil
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
