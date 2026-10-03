// Package qa kontroluje hotový short před publikací (plán 3.7).
package qa

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/ol1n/memeshorts/internal/ffmpeg"
)

type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
	Skip   bool   `json:"skipped,omitempty"`
}

type Report struct {
	Passed    bool    `json:"passed"`
	DurationS float64 `json:"duration_s"`
	Checks    []Check `json:"checks"`
}

func (r Report) Failures() []string {
	var out []string
	for _, c := range r.Checks {
		if !c.OK && !c.Skip {
			out = append(out, c.Name+": "+c.Detail)
		}
	}
	return out
}

type probe struct {
	Streams []struct {
		CodecType  string `json:"codec_type"`
		CodecName  string `json:"codec_name"`
		Width      int    `json:"width"`
		Height     int    `json:"height"`
		RFrameRate string `json:"r_frame_rate"`
		PixFmt     string `json:"pix_fmt"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

// Run ověří formát, délku, zvuk a černé snímky.
func Run(ctx context.Context, path string) (Report, error) {
	raw, err := ffmpeg.ProbeJSON(ctx, path)
	if err != nil {
		return Report{}, err
	}
	var p probe
	if err := json.Unmarshal(raw, &p); err != nil {
		return Report{}, err
	}
	var r Report
	r.DurationS, _ = strconv.ParseFloat(p.Format.Duration, 64)
	add := func(name string, ok bool, detail string) {
		r.Checks = append(r.Checks, Check{Name: name, OK: ok, Detail: detail})
	}

	add("duration", r.DurationS >= 5 && r.DurationS <= 15.05, fmt.Sprintf("%.2f s (povoleno 5–15)", r.DurationS))

	var hasV, hasA bool
	for _, s := range p.Streams {
		switch s.CodecType {
		case "video":
			hasV = true
			add("resolution", s.Width == 1080 && s.Height == 1920, fmt.Sprintf("%dx%d", s.Width, s.Height))
			add("video_codec", s.CodecName == "h264" && s.PixFmt == "yuv420p", s.CodecName+"/"+s.PixFmt)
			add("fps", s.RFrameRate == "30/1", s.RFrameRate)
		case "audio":
			hasA = true
			add("audio_codec", s.CodecName == "aac", s.CodecName)
		}
	}
	add("video_stream", hasV, "")
	add("audio_stream", hasA, "")

	black, err := blackSegments(ctx, path)
	if err != nil {
		return r, err
	}
	add("no_black_frames", len(black) == 0, strings.Join(black, ", "))

	lufs, err := loudness(ctx, path)
	if err != nil {
		return r, err
	}
	add("loudness", lufs >= TargetLUFS-LUFSTolerance && lufs <= TargetLUFS+LUFSTolerance,
		fmt.Sprintf("%.1f LUFS (cíl %d ±%.1f)", lufs, TargetLUFS, LUFSTolerance))

	// OCR titulků vs. storyboard — čeká na napojení OCR service (ocr-api na Sparku).
	r.Checks = append(r.Checks, Check{Name: "caption_ocr", Skip: true, Detail: "OCR zatím nenapojeno"})

	r.Passed = len(r.Failures()) == 0
	return r, nil
}

const (
	TargetLUFS    = -14
	LUFSTolerance = 1.5
)

func loudness(ctx context.Context, path string) (float64, error) {
	log, err := ffmpeg.RunStderr(ctx, "-i", path, "-vn", "-af", "ebur128", "-f", "null", "-")
	if err != nil {
		return 0, err
	}
	// Souhrn na konci logu: "Integrated loudness:\n    I:  -13.8 LUFS"
	i := strings.LastIndex(log, "Integrated loudness:")
	if i < 0 {
		return 0, fmt.Errorf("ebur128: chybí souhrn")
	}
	var v float64
	for _, line := range strings.Split(log[i:], "\n")[1:] {
		f := strings.Fields(line)
		if len(f) >= 2 && f[0] == "I:" {
			if f[1] == "-inf" {
				return -999, nil
			}
			v, err = strconv.ParseFloat(f[1], 64)
			return v, err
		}
	}
	return 0, fmt.Errorf("ebur128: nečitelný souhrn")
}

func blackSegments(ctx context.Context, path string) ([]string, error) {
	log, err := ffmpeg.RunStderr(ctx, "-i", path, "-vf", "blackdetect=d=0.2:pix_th=0.08", "-an", "-f", "null", "-")
	if err != nil {
		return nil, err
	}
	var out []string
	for _, line := range strings.Split(log, "\n") {
		if i := strings.Index(line, "black_start:"); i >= 0 {
			out = append(out, strings.TrimSpace(line[i:]))
		}
	}
	return out, nil
}
