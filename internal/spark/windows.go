// Package spark zná rozvrh SPARKu (AiStack PLAN-spark-scheduler.md §3), aby klienti
// mimo okna nic neposílali. Dávky se navíc hlásí session Director předem.
package spark

import (
	"context"
	"fmt"
	"time"
)

const (
	ComfyOpen, ComfyClose = 7, 13 // ComfyUI jen ráno, primárně pro experimenty uživatele
	NIMWarmupMin          = 15    // minut po přepnutí profilu (07:00, 19:00), než flux-schnell odpovídá
)

// NIMOpen: flux-schnell NIM běží 07–13 a 19–01.
func NIMOpen(now time.Time) bool {
	h := now.Hour()
	return (h >= ComfyOpen && h < ComfyClose) || h >= 19 || h < 1
}

// NIMWait vrátí chybu mimo okno; v první čtvrthodině okna počká, než NIM naběhne.
func NIMWait(ctx context.Context, now time.Time, log func(string, ...any)) error {
	if !NIMOpen(now) {
		return fmt.Errorf("mimo okna flux-schnell (07–13, 19–01), je %s", now.Format("15:04"))
	}
	h := now.Hour()
	if (h == ComfyOpen || h == 19) && now.Minute() < NIMWarmupMin {
		ready := time.Date(now.Year(), now.Month(), now.Day(), h, NIMWarmupMin, 0, 0, now.Location())
		if log != nil {
			log("NIM nabíhá, čekám do %s", ready.Format("15:04"))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(ready.Sub(now)):
		}
	}
	return nil
}

// ComfyLeft vrátí, kolik zbývá z okna ComfyUI; chyba mimo okno nebo když zbývá míň než need.
func ComfyLeft(now time.Time, need time.Duration) (time.Duration, error) {
	closeAt := time.Date(now.Year(), now.Month(), now.Day(), ComfyClose, 0, 0, 0, now.Location())
	if now.Hour() < ComfyOpen || !now.Before(closeAt) {
		return 0, fmt.Errorf("mimo okno ComfyUI (%02d–%02d), je %s", ComfyOpen, ComfyClose, now.Format("15:04"))
	}
	left := closeAt.Sub(now)
	if left < need {
		return left, fmt.Errorf("do konce okna zbývá %s, potřeba %s", left.Round(time.Minute), need)
	}
	return left, nil
}
