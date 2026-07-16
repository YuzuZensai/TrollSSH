package main

import (
	"path/filepath"
	"sync/atomic"
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
	b.Cleanup(func() { _ = fc.Close() })
	return fc
}

func benchRender(b *testing.B, tier colorTier, w, h int) {
	fc := loadBenchSet(b)
	r := newFrameRenderer(0, fc.ColorFrames, asciiOptions{
		brightnessThreshold: 40,
		charset:             "detailed",
	}, nil)
	b.ReportAllocs()
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

func BenchmarkRenderCachedParallel(b *testing.B) {
	fc := loadBenchSet(b)
	r := newFrameRenderer(0, fc.ColorFrames, asciiOptions{
		brightnessThreshold: 40,
		charset:             "detailed",
	}, newRenderCache(8<<20))
	frame, err := r.render(0, 120, 40, false, colorTierTrueColor)
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(frame)))
	b.ReportAllocs()
	var failures atomic.Uint64
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := r.render(0, 120, 40, false, colorTierTrueColor); err != nil {
				failures.Add(1)
			}
		}
	})
	if failures.Load() != 0 {
		b.Fatalf("render failures: %d", failures.Load())
	}
}
