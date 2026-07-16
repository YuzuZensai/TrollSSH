package render

import (
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/YuzuZensai/TrollSSH/internal/tsf"
)

func loadBenchSet(b *testing.B) *tsf.FramesContainer {
	b.Helper()
	matches, _ := filepath.Glob("../../frames/*.tsf")
	if len(matches) == 0 {
		b.Skip("no .tsf frame set in ../../frames")
	}
	fc, err := tsf.Load(matches[0])
	if err != nil {
		b.Skip("failed to load frame set:", err)
	}
	b.Cleanup(func() { _ = fc.Close() })
	return fc
}

func benchRender(b *testing.B, tier ColorTier, w, h int) {
	fc := loadBenchSet(b)
	r := NewRenderer(0, fc.ColorFrames, Options{
		BrightnessThreshold: 40,
		Charset:             "detailed",
	}, nil)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := r.Render(i%len(fc.ColorFrames), w, h, false, tier); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkRenderTrueColor(b *testing.B) { benchRender(b, ColorTierTrueColor, 120, 40) }
func BenchmarkRender256(b *testing.B)       { benchRender(b, ColorTier256, 120, 40) }
func BenchmarkRenderGray(b *testing.B)      { benchRender(b, ColorTierNone, 120, 40) }
func BenchmarkRenderTrueBig(b *testing.B)   { benchRender(b, ColorTierTrueColor, 240, 70) }

func BenchmarkRenderCachedParallel(b *testing.B) {
	fc := loadBenchSet(b)
	r := NewRenderer(0, fc.ColorFrames, Options{
		BrightnessThreshold: 40,
		Charset:             "detailed",
	}, NewCache(8<<20))
	frame, err := r.Render(0, 120, 40, false, ColorTierTrueColor)
	if err != nil {
		b.Fatal(err)
	}
	b.SetBytes(int64(len(frame)))
	b.ReportAllocs()
	var failures atomic.Uint64
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if _, err := r.Render(0, 120, 40, false, ColorTierTrueColor); err != nil {
				failures.Add(1)
			}
		}
	})
	if failures.Load() != 0 {
		b.Fatalf("render failures: %d", failures.Load())
	}
}
