// Package caption renderuje titulky a štítky do průhledných PNG pro ffmpeg overlay.
// Nezávisí na drawtext/libfreetype ve ffmpeg buildu (Homebrew ho nemá).
package caption

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"math"
	"os"
	"strings"
	"sync"

	"golang.org/x/image/font"
	"golang.org/x/image/font/opentype"
	"golang.org/x/image/math/fixed"
)

type Renderer struct {
	font *opentype.Font
	mu   sync.Mutex
	face map[float64]font.Face
}

func NewRenderer(fontPath string) (*Renderer, error) {
	data, err := os.ReadFile(fontPath)
	if err != nil {
		return nil, err
	}
	f, err := opentype.Parse(data)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", fontPath, err)
	}
	return &Renderer{font: f, face: map[float64]font.Face{}}, nil
}

func (r *Renderer) faceAt(size float64) (font.Face, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if f, ok := r.face[size]; ok {
		return f, nil
	}
	f, err := opentype.NewFace(r.font, &opentype.FaceOptions{Size: size, DPI: 72, Hinting: font.HintingFull})
	if err != nil {
		return nil, err
	}
	r.face[size] = f
	return f, nil
}

type Style struct {
	Width     int // šířka plátna PNG
	MaxLines  int
	MaxSize   float64
	MinSize   float64
	Fill      color.Color
	Stroke    color.Color
	StrokePx  int
	Uppercase bool
	LineGap   float64 // násobek výšky řádku
}

// MemeStyle — bílý tučný text s černým obrysem (plán 3.6).
func MemeStyle(width int) Style {
	return Style{Width: width, MaxLines: 4, MaxSize: 120, MinSize: 60,
		Fill: color.White, Stroke: color.Black, StrokePx: 7, Uppercase: true, LineGap: 1.08}
}

// Render vrátí obrázek s textem zarovnaným na střed; výška podle počtu řádků.
// Velikost písma se zmenšuje, dokud se text nevejde do MaxLines.
func (r *Renderer) Render(text string, st Style) (*image.RGBA, error) {
	if st.Uppercase {
		text = strings.ToUpper(text)
	}
	pad := st.StrokePx + 24
	avail := st.Width - 2*pad
	var face font.Face
	var lines []string
	for size := st.MaxSize; ; size -= 4 {
		f, err := r.faceAt(size)
		if err != nil {
			return nil, err
		}
		face, lines = f, wrap(f, text, avail)
		if (len(lines) <= st.MaxLines && fits(f, lines, avail)) || size-4 < st.MinSize {
			break
		}
	}
	m := face.Metrics()
	lineH := int(math.Ceil(float64((m.Ascent + m.Descent).Ceil()) * st.LineGap))
	h := lineH*len(lines) + 2*pad
	img := image.NewRGBA(image.Rect(0, 0, st.Width, h))
	stroke := strokeOffsets(st.StrokePx)
	for i, line := range lines {
		w := font.MeasureString(face, line).Ceil()
		x := (st.Width - w) / 2
		y := pad + i*lineH + m.Ascent.Ceil()
		for _, o := range stroke {
			drawString(img, face, line, x+o.X, y+o.Y, st.Stroke)
		}
		drawString(img, face, line, x, y, st.Fill)
	}
	return img, nil
}

// Badge — malý štítek (např. „AI GENERATED", EU AI Act) s poloprůhledným pozadím.
func (r *Renderer) Badge(text string, size float64) (*image.RGBA, error) {
	face, err := r.faceAt(size)
	if err != nil {
		return nil, err
	}
	m := face.Metrics()
	padX, padY := int(size*0.6), int(size*0.3)
	w := font.MeasureString(face, text).Ceil() + 2*padX
	h := (m.Ascent + m.Descent).Ceil() + 2*padY
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	draw.Draw(img, img.Bounds(), &image.Uniform{color.NRGBA{0, 0, 0, 150}}, image.Point{}, draw.Src)
	drawString(img, face, text, padX, padY+m.Ascent.Ceil(), color.NRGBA{255, 255, 255, 230})
	return img, nil
}

func wrap(face font.Face, text string, maxW int) []string {
	var lines []string
	cur := ""
	for _, w := range strings.Fields(text) {
		try := w
		if cur != "" {
			try = cur + " " + w
		}
		if cur != "" && font.MeasureString(face, try).Ceil() > maxW {
			lines = append(lines, cur)
			cur = w
		} else {
			cur = try
		}
	}
	if cur != "" {
		lines = append(lines, cur)
	}
	return lines
}

func fits(face font.Face, lines []string, maxW int) bool {
	for _, l := range lines {
		if font.MeasureString(face, l).Ceil() > maxW {
			return false
		}
	}
	return true
}

func strokeOffsets(r int) []image.Point {
	var pts []image.Point
	for dy := -r; dy <= r; dy++ {
		for dx := -r; dx <= r; dx++ {
			if d := dx*dx + dy*dy; d <= r*r && d > 0 {
				pts = append(pts, image.Point{dx, dy})
			}
		}
	}
	return pts
}

func drawString(dst draw.Image, face font.Face, s string, x, y int, c color.Color) {
	d := font.Drawer{Dst: dst, Src: image.NewUniform(c), Face: face, Dot: fixed.P(x, y)}
	d.DrawString(s)
}

func SavePNG(img image.Image, path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	if err := png.Encode(f, img); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}
