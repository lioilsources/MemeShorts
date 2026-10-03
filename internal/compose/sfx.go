package compose

import (
	"context"
	"os"
	"path/filepath"

	"github.com/ol1n/memeshorts/internal/ffmpeg"
)

// Syntetické SFX — licenčně čisté z principu (plán §0: jen vlastní knihovna).
// Vlastní nahrávka se stejným jménem v assets/sfx má přednost.
var sfxSynth = map[string]string{
	// Basový „boom": sweep 120→40 Hz s exponenciálním doznějem + šum úderu.
	"boom": "aevalsrc='0.95*sin(2*PI*(40*t+80*(1-exp(-6*t))/6))*exp(-3.5*t)+0.25*(random(0)-0.5)*exp(-40*t)':s=48000:d=1.2",
	// „Whoosh": růžový šum s obálkou a pásmovou propustí.
	"whoosh": "anoisesrc=d=0.45:c=pink:a=0.6:r=48000,bandpass=f=1800:width_type=o:w=2,afade=t=in:d=0.2,afade=t=out:st=0.2:d=0.25",
}

// EnsureSFX vrátí cesty k SFX a chybějící vygeneruje do dir.
func EnsureSFX(ctx context.Context, dir string) (map[string]string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for name, src := range sfxSynth {
		p := filepath.Join(dir, name+".wav")
		if st, err := os.Stat(p); err != nil || st.Size() == 0 {
			if err := ffmpeg.Run(ctx, "-f", "lavfi", "-i", src, "-ac", "2", p); err != nil {
				return nil, err
			}
		}
		out[name] = p
	}
	return out, nil
}
