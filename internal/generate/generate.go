// Package generate vyrábí obrázek vlastní postavy pro každou scénu storyboardu.
// Originál memu se sem nikdy nedostane — je jen předlohou struktury vtipu.
package generate

import (
	"context"
	"hash/fnv"
	"image"
	"image/color"
	"image/draw"
	"strconv"
	"strings"

	"github.com/ol1n/memeshorts/internal/caption"
	"github.com/ol1n/memeshorts/internal/storyboard"
)

// Obrázky postav jsou na výšku 832×1216 (nativní pro SDXL i Wan, viz plán §1).
const (
	W = 832
	H = 1216
)

type Provider interface {
	Name() string
	Generate(ctx context.Context, sc storyboard.Scene, outPNG string) error
}

// Seed je stabilní pro postavu — stejný seed drží vzhled napříč scénami
// lépe než náhodný (plnou identitu řeší IP-Adapter v M2).
func Seed(characterKey string) int64 {
	h := fnv.New32a()
	h.Write([]byte(characterKey))
	return int64(h.Sum32() & 0x7fffffff)
}

// Placeholder kreslí barevnou kartu postavy — pro běh roury bez GPU (M1, testy).
type Placeholder struct{ Captions *caption.Renderer }

func (Placeholder) Name() string { return "placeholder" }

func (p Placeholder) Generate(_ context.Context, sc storyboard.Scene, out string) error {
	base := parseHex(sc.Character.Color, color.RGBA{90, 90, 120, 255})
	img := image.NewRGBA(image.Rect(0, 0, W, H))
	for y := 0; y < H; y++ {
		t := float64(y) / H
		c := color.RGBA{mix(base.R, 20, t), mix(base.G, 20, t), mix(base.B, 30, t), 255}
		draw.Draw(img, image.Rect(0, y, W, y+1), &image.Uniform{c}, image.Point{}, draw.Src)
	}
	// Silueta: hlava + ramena, ať je v kenburns vidět pohyb.
	sil := color.RGBA{mix(base.R, 255, .35), mix(base.G, 255, .35), mix(base.B, 255, .35), 255}
	fillCircle(img, W/2, H*34/100, W*19/100, sil)
	fillCircle(img, W/2, H*100/100, W*48/100, sil)

	name, err := p.Captions.Render(sc.Character.Name, caption.Style{Width: W, MaxLines: 1, MaxSize: 120, MinSize: 60,
		Fill: color.White, Stroke: color.Black, StrokePx: 5, Uppercase: true, LineGap: 1})
	if err != nil {
		return err
	}
	// Popisky dole přes „tělo" — nahoře by kolidovaly s titulkem.
	nameY := H * 56 / 100
	draw.Draw(img, name.Bounds().Add(image.Pt(0, nameY)), name, image.Point{}, draw.Over)
	emo, err := p.Captions.Render("("+sc.Emotion+")", caption.Style{Width: W, MaxLines: 1, MaxSize: 64, MinSize: 40,
		Fill: color.White, Stroke: color.Black, StrokePx: 4, LineGap: 1})
	if err != nil {
		return err
	}
	draw.Draw(img, emo.Bounds().Add(image.Pt(0, nameY+name.Bounds().Dy()-20)), emo, image.Point{}, draw.Over)
	return caption.SavePNG(img, out)
}

func mix(a, b uint8, t float64) uint8 { return uint8(float64(a)*(1-t) + float64(b)*t) }

func fillCircle(img *image.RGBA, cx, cy, r int, c color.Color) {
	for y := cy - r; y <= cy+r; y++ {
		for x := cx - r; x <= cx+r; x++ {
			if (x-cx)*(x-cx)+(y-cy)*(y-cy) <= r*r && image.Pt(x, y).In(img.Rect) {
				img.Set(x, y, c)
			}
		}
	}
}

func parseHex(s string, fallback color.RGBA) color.RGBA {
	s = strings.TrimPrefix(s, "#")
	if len(s) != 6 {
		return fallback
	}
	v, err := strconv.ParseUint(s, 16, 32)
	if err != nil {
		return fallback
	}
	return color.RGBA{uint8(v >> 16), uint8(v >> 8), uint8(v), 255}
}
