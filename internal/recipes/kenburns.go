package recipes

import (
	"context"
	"fmt"
	"strconv"

	"github.com/ol1n/memeshorts/internal/ffmpeg"
	"github.com/ol1n/memeshorts/internal/storyboard"
)

// KenBurns — zoompan nad statickým obrázkem, ~1 s CPU na scénu.
type KenBurns struct{}

func (KenBurns) Name() string { return "kenburns" }

// Zoompan zaokrouhluje x/y na celé pixely vstupu → na malém vstupu se obraz třese.
// Vstup proto nejdřív zvětšíme na 3× výstup (a dorovnáme poměr stran ořezem).
const kbUpscale = 3

const hookFrames = 24 // 0.8 s při 30 fps

func (KenBurns) Animate(ctx context.Context, sc storyboard.Scene, img, out string, hook bool) error {
	if sc.Frames <= 0 {
		return fmt.Errorf("kenburns: scéna %d nemá délku", sc.Index)
	}
	z, x, y := cameraExpr(sc.Camera, sc.Frames)
	if hook {
		// Rychlý odjezd z 1.35 na trajektorii kamery, ease-out.
		z = fmt.Sprintf("if(lt(on,%[1]d),(%[2]s)+0.35*pow(1-on/%[1]d,2),%[2]s)", hookFrames, z)
	}
	uw, uh := kbUpscale*ClipW, kbUpscale*ClipH
	vf := fmt.Sprintf("scale=%d:%d:flags=lanczos:force_original_aspect_ratio=increase,crop=%d:%d,zoompan=z='%s':x='%s':y='%s':d=%d:s=%dx%d:fps=%d,format=yuv420p",
		uw, uh, uw, uh, z, x, y, sc.Frames, ClipW, ClipH, storyboard.FPS)
	return ffmpeg.Run(ctx, "-i", img, "-vf", vf, "-frames:v", strconv.Itoa(sc.Frames),
		"-an", "-c:v", "libx264", "-preset", "veryfast", "-crf", "14", out)
}

// cameraExpr vrací zoompan výrazy z, x, y. `on` = číslo výstupního snímku.
func cameraExpr(camera string, frames int) (z, x, y string) {
	n := strconv.Itoa(max(frames-1, 1))
	center := func() (string, string) { return "iw/2-(iw/zoom/2)", "ih/2-(ih/zoom/2)" }
	switch camera {
	case "pull_out":
		z = "1.18-0.16*on/" + n
		x, y = center()
	case "pan_right", "pan_left":
		z = "1.15"
		p := "on/" + n
		if camera == "pan_left" {
			p = "(1-on/" + n + ")"
		}
		x = "(iw-iw/zoom)*" + p
		y = "ih/2-(ih/zoom/2)"
	case "punch_zoom":
		// Prudký nájezd za 5 snímků, pak pomalý dojezd + lehké třesení pro důraz.
		z = "if(lt(on,5),1+0.24*on/5,1.24+0.06*(on-5)/" + n + ")"
		x = "iw/2-(iw/zoom/2)+if(lt(on,12),6*sin(on*2.5),0)"
		y = "ih/2-(ih/zoom/2)+if(lt(on,12),6*cos(on*3.1),0)"
	default: // push_in
		z = "1+0.15*on/" + n
		x, y = center()
	}
	return z, x, y
}
