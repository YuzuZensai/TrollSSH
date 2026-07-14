package main

import (
	"path/filepath"
	"testing"
)

func loadBenchSet(b *testing.B) *FramesContainer {
	b.Helper()
	matches, _ := filepath.Glob("../frames/*.tsf")
	if len(matches) == 0 {
		b.Skip("no .tsf frame set in ../frames")
	}
	fc, err := loadTSF(matches[0])
	if err != nil {
		b.Skip("failed to load frame set:", err)
	}
	return fc
}

func benchRender(b *testing.B, tier colorTier, w, h int) {
	fc := loadBenchSet(b)
	r := newFrameRenderer(0, fc.ColorFrames, asciiOptions{
		brightnessThreshold: 40,
		charset:             "detailed",
	}, nil)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := r.render(i%len(fc.ColorFrames), w, h, false, tier); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRenderTrueColor(b *testing.B) { benchRender(b, colorTierTrueColor, 120, 40) }
func BenchmarkRender256(b *testing.B)       { benchRender(b, colorTier256, 120, 40) }
func BenchmarkRenderGray(b *testing.B)      { benchRender(b, colorTierNone, 120, 40) }
func BenchmarkRenderTrueBig(b *testing.B)   { benchRender(b, colorTierTrueColor, 240, 70) }
