// Package ffmpeg spouští ffmpeg/ffprobe a při chybě vrací konec stderr.
package ffmpeg

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
)

var (
	Bin   = "ffmpeg"
	Probe = "ffprobe"
)

// Run spustí ffmpeg s -y -hide_banner -loglevel error.
func Run(ctx context.Context, args ...string) error {
	_, err := run(ctx, Bin, append([]string{"-y", "-hide_banner", "-nostdin", "-loglevel", "error"}, args...)...)
	return err
}

// RunStderr spustí ffmpeg a vrátí stderr (pro filtry, které reportují do logu, např. blackdetect).
func RunStderr(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, Bin, append([]string{"-hide_banner", "-nostdin"}, args...)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return stderr.String(), fmt.Errorf("ffmpeg: %w: %s", err, tail(stderr.String()))
	}
	return stderr.String(), nil
}

func ProbeJSON(ctx context.Context, path string) ([]byte, error) {
	return run(ctx, Probe, "-v", "error", "-print_format", "json", "-show_format", "-show_streams", path)
}

func run(ctx context.Context, bin string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, bin, args...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("%s: %w: %s", bin, err, tail(stderr.String()))
	}
	return stdout.Bytes(), nil
}

func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 600 {
		s = "…" + s[len(s)-600:]
	}
	return s
}

// Sec formátuje sekundy pro ffmpeg argumenty.
func Sec(ms int) string { return fmt.Sprintf("%.3f", float64(ms)/1000) }
