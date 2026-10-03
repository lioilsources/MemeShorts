// Package recipes rozpohybuje statický obrázek scény do čtvercového klipu (plán 3.5).
package recipes

import (
	"context"
	"fmt"
	"sort"

	"github.com/ol1n/memeshorts/internal/storyboard"
)

// Klip postavy na výšku (knihovna je 832×1216 → šířka plátna 1080); compose ho posadí na spodek 9:16 plátna.
const ClipW, ClipH = 1080, 1578

type Recipe interface {
	Name() string
	// Animate vyrobí němý klip ClipW×ClipH o přesně sc.Frames snímcích.
	// hook=true u první scény: rychlý zoom v prvních 0.8 s (plán 3.6).
	Animate(ctx context.Context, sc storyboard.Scene, imgPath, outMP4 string, hook bool) error
}

var registry = map[string]Recipe{
	"kenburns": KenBurns{},
}

// Plánované recepty — chybová hláška ukáže milník, ve kterém přibudou.
var planned = map[string]string{"i2v": "M2", "dialog": "M3", "panels": "M2+"}

func Get(name string) (Recipe, error) {
	if r, ok := registry[name]; ok {
		return r, nil
	}
	if m, ok := planned[name]; ok {
		return nil, fmt.Errorf("recept %q zatím není (plán: %s)", name, m)
	}
	return nil, fmt.Errorf("neznámý recept %q (dostupné: %v)", name, Names())
}

func Names() []string {
	var out []string
	for n := range registry {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}
