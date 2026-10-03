// Package compose skládá klipy scén do finálního 9:16 shortu (plán 3.6):
// rozmazané pozadí, titulky s pop-in, AI badge, SFX, loudness -14 LUFS.
package compose

import (
	"context"
	"encoding/json"
	"fmt"
	"image"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/ol1n/memeshorts/internal/caption"
	"github.com/ol1n/memeshorts/internal/ffmpeg"
	"github.com/ol1n/memeshorts/internal/recipes"
	"github.com/ol1n/memeshorts/internal/storyboard"
)

const (
	W, H = 1080, 1920
	// Klip postavy na výšku sedí na spodku plátna; nad hlavou má vlastní pozadí, do kterého smí titulek zasahovat.
	clipY = H - recipes.ClipH
	// Pás titulku: od bezpečné zóny nahoře po úroveň hlavy postavy.
	captionTop     = 110
	captionBottom  = 560
	badgeX, badgeY = 40, H - 300
	LoudnessLUFS   = -14
)

type SceneInput struct {
	Scene storyboard.Scene
	Image string // vygenerovaný obrázek postavy (pozadí)
	Clip  string // animovaný klip z receptu
}

type Composer struct {
	Captions *caption.Renderer
	SFXDir   string // assets/sfx — boom.wav, whoosh.wav (chybějící se syntetizují)
	MusicDir string // assets/music — volitelný podkres (royalty-free, vlastní knihovna)
	Title    string // metadata
}

// Compose vyrobí out (MP4) do pracovního adresáře work.
func (c *Composer) Compose(ctx context.Context, scenes []SceneInput, work, out string) error {
	if len(scenes) == 0 {
		return fmt.Errorf("compose: žádné scény")
	}
	if err := os.MkdirAll(work, 0o755); err != nil {
		return err
	}
	badge, err := c.Captions.Badge("AI GENERATED", 30)
	if err != nil {
		return err
	}
	badgePath := filepath.Join(work, "badge.png")
	if err := caption.SavePNG(badge, badgePath); err != nil {
		return err
	}

	var segs []string
	totalFrames := 0
	for i, s := range scenes {
		seg := filepath.Join(work, fmt.Sprintf("seg_%02d.mp4", i))
		if err := c.segment(ctx, s, badgePath, work, seg); err != nil {
			return fmt.Errorf("scéna %d: %w", i, err)
		}
		segs = append(segs, seg)
		totalFrames += s.Scene.Frames
	}
	list := filepath.Join(work, "concat.txt")
	var b strings.Builder
	for _, s := range segs {
		fmt.Fprintf(&b, "file '%s'\n", filepath.Base(s))
	}
	if err := os.WriteFile(list, []byte(b.String()), 0o644); err != nil {
		return err
	}
	return c.final(ctx, scenes, list, totalFrames, out)
}

// segment: pozadí (blur obrázku) + klip + titulek + badge → 1080×1920, bez zvuku.
func (c *Composer) segment(ctx context.Context, s SceneInput, badge, work, out string) error {
	capImg, err := c.Captions.Render(s.Scene.Text, caption.MemeStyle(W))
	if err != nil {
		return err
	}
	capPath := filepath.Join(work, fmt.Sprintf("caption_%02d.png", s.Scene.Index))
	if err := caption.SavePNG(capImg, capPath); err != nil {
		return err
	}
	capY := captionY(capImg.Bounds())
	dur := ffmpeg.Sec(s.Scene.DurationMs)
	frames := strconv.Itoa(s.Scene.Frames)
	fc := strings.Join([]string{
		fmt.Sprintf("[1:v]scale=%[1]d:%[1]d,crop=%d:%d,gblur=sigma=45,eq=brightness=-0.18:saturation=1.2[bg]", H, W, H),
		fmt.Sprintf("[0:v]scale=%d:%d,setsar=1[clip]", recipes.ClipW, recipes.ClipH),
		fmt.Sprintf("[bg][clip]overlay=(W-w)/2:%d[v1]", clipY),
		// Pop-in: titulek naběhne průhledností za 0.18 s, s malým zpožděním po střihu.
		"[2:v]format=rgba,fade=in:st=0.08:d=0.18:alpha=1[cap]",
		fmt.Sprintf("[v1][cap]overlay=0:%d[v2]", capY),
		fmt.Sprintf("[v2][3:v]overlay=%d:%d,fps=%d,format=yuv420p[v]", badgeX, badgeY, storyboard.FPS),
	}, ";")
	fps := strconv.Itoa(storyboard.FPS)
	return ffmpeg.Run(ctx,
		"-i", s.Clip,
		"-loop", "1", "-framerate", fps, "-t", dur, "-i", s.Image,
		"-loop", "1", "-framerate", fps, "-t", dur, "-i", capPath,
		"-loop", "1", "-framerate", fps, "-t", dur, "-i", badge,
		"-filter_complex", fc, "-map", "[v]", "-frames:v", frames,
		"-an", "-c:v", "libx264", "-preset", "veryfast", "-crf", "16", out)
}

// captionY vystředí titulek v pásu nad klipem; vyšší titulek přesahuje do klipu.
func captionY(b image.Rectangle) int {
	return max(captionTop, captionTop+(captionBottom-captionTop-b.Dy())/2)
}

// final: zvuková stopa (ticho jako nosná, SFX, podkres) → dvouprůchodový loudnorm → mux s videem.
func (c *Composer) final(ctx context.Context, scenes []SceneInput, list string, totalFrames int, out string) error {
	totalMs := totalFrames * 1000 / storyboard.FPS
	mixWav := filepath.Join(filepath.Dir(list), "mix.wav")
	if err := c.mixAudio(ctx, scenes, totalMs, mixWav); err != nil {
		return fmt.Errorf("audio mix: %w", err)
	}
	ln, err := measureLoudness(ctx, mixWav)
	if err != nil {
		return err
	}
	return ffmpeg.Run(ctx,
		"-f", "concat", "-safe", "0", "-i", list, "-i", mixWav,
		"-map", "0:v", "-map", "1:a",
		"-af", ln+",aresample=48000",
		"-c:v", "libx264", "-profile:v", "high", "-preset", "medium", "-crf", "20", "-pix_fmt", "yuv420p",
		"-r", strconv.Itoa(storyboard.FPS), "-frames:v", strconv.Itoa(totalFrames),
		"-c:a", "aac", "-b:a", "192k", "-ar", "48000", "-t", ffmpeg.Sec(totalMs),
		"-movflags", "+faststart",
		"-metadata", "title="+c.Title,
		"-metadata", "comment=AI generated content (MemeShorts). Original characters; meme format only.",
		"-metadata", "ai_generated=true",
		out)
}

// mixAudio: whoosh na každém střihu, boom na punchline, volitelně podkres.
// Každé SFX je samostatný vstup — amix nad asplit tiše zahazuje data (viz AiStack CLAUDE.md).
func (c *Composer) mixAudio(ctx context.Context, scenes []SceneInput, totalMs int, out string) error {
	sfx, err := EnsureSFX(ctx, c.SFXDir)
	if err != nil {
		return err
	}
	total := ffmpeg.Sec(totalMs)
	args := []string{"-f", "lavfi", "-t", total, "-i", "anullsrc=r=48000:cl=stereo"}
	var mixIn, filters []string
	in := 1
	startMs := 0
	for i, s := range scenes {
		file, gain := "", 1.0
		switch {
		case s.Scene.Punchline:
			file, gain = sfx["boom"], 0.9
		case i > 0:
			file, gain = sfx["whoosh"], 0.5
		}
		if file != "" {
			args = append(args, "-i", file)
			label := fmt.Sprintf("s%d", in)
			filters = append(filters, fmt.Sprintf("[%d:a]aresample=48000,aformat=channel_layouts=stereo,volume=%.2f,adelay=%d:all=1[%s]", in, gain, startMs, label))
			mixIn = append(mixIn, "["+label+"]")
			in++
		}
		startMs += s.Scene.Frames * 1000 / storyboard.FPS
	}
	if m := pickMusic(c.MusicDir); m != "" {
		args = append(args, "-stream_loop", "-1", "-i", m)
		filters = append(filters, fmt.Sprintf("[%d:a]aresample=48000,aformat=channel_layouts=stereo,volume=0.12,afade=out:st=%s:d=0.8[mus]", in, ffmpeg.Sec(max(totalMs-800, 0))))
		mixIn = append(mixIn, "[mus]")
	}
	filters = append(filters, fmt.Sprintf("[0:a]%samix=inputs=%d:duration=first:normalize=0,atrim=0:%s[a]",
		strings.Join(mixIn, ""), len(mixIn)+1, total))
	args = append(args, "-filter_complex", strings.Join(filters, ";"), "-map", "[a]", "-c:a", "pcm_s16le", out)
	return ffmpeg.Run(ctx, args...)
}

// measureLoudness — první průchod loudnorm; vrací filtr pro druhý (lineární) průchod.
func measureLoudness(ctx context.Context, wav string) (string, error) {
	target := fmt.Sprintf("loudnorm=I=%d:TP=-1.5:LRA=11", LoudnessLUFS)
	log, err := ffmpeg.RunStderr(ctx, "-i", wav, "-af", target+":print_format=json", "-f", "null", "-")
	if err != nil {
		return "", err
	}
	i := strings.LastIndex(log, "{")
	j := strings.LastIndex(log, "}")
	if i < 0 || j < i {
		return "", fmt.Errorf("loudnorm: chybí měření")
	}
	var m struct {
		I      string `json:"input_i"`
		TP     string `json:"input_tp"`
		LRA    string `json:"input_lra"`
		Thresh string `json:"input_thresh"`
		Offset string `json:"target_offset"`
	}
	if err := json.Unmarshal([]byte(log[i:j+1]), &m); err != nil {
		return "", fmt.Errorf("loudnorm: %w", err)
	}
	if m.I == "-inf" { // samé ticho — není co normalizovat
		return "anull", nil
	}
	return fmt.Sprintf("%s:measured_I=%s:measured_TP=%s:measured_LRA=%s:measured_thresh=%s:offset=%s:linear=true",
		target, m.I, m.TP, m.LRA, m.Thresh, m.Offset), nil
}

func pickMusic(dir string) string {
	if dir == "" {
		return ""
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	var files []string
	for _, e := range entries {
		switch strings.ToLower(filepath.Ext(e.Name())) {
		case ".mp3", ".wav", ".m4a", ".flac", ".ogg":
			files = append(files, filepath.Join(dir, e.Name()))
		}
	}
	sort.Strings(files)
	if len(files) == 0 {
		return ""
	}
	return files[0]
}
