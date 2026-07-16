package render

import (
	"bytes"
	"image"
	"image/jpeg"
	"strings"
	"sync"
	"testing"
)

func TestResolveCharset(t *testing.T) {
	if got := resolveCharset("blocks"); got != " ░▒▓█" {
		t.Errorf("blocks preset = %q", got)
	}
	if got := resolveCharset("XYZ"); got != "XYZ" {
		t.Errorf("custom ramp = %q", got)
	}
	if got := resolveCharset(""); !strings.HasPrefix(got, " .") {
		t.Errorf("default = %q", got)
	}
}

func TestFrameToAscii(t *testing.T) {
	// Below threshold -> first ramp char; full brightness -> last.
	opts := Options{BrightnessThreshold: 40, Charset: "standard"}
	ramp := []rune(resolveCharset("standard"))
	img := &image.RGBA{
		Pix:    []byte{0, 0, 0, 255, 255, 255, 255, 255},
		Stride: 8, Rect: image.Rect(0, 0, 2, 1),
	}
	out := []rune(string(frameToAscii(img, buildRampLUT(ramp, opts))))
	if out[0] != ramp[0] {
		t.Errorf("dark px = %q, want %q", out[0], ramp[0])
	}
	if out[1] != ramp[len(ramp)-1] {
		t.Errorf("bright px = %q, want %q", out[1], ramp[len(ramp)-1])
	}
}

func TestFrameToAsciiInvert(t *testing.T) {
	opts := Options{BrightnessThreshold: 40, Charset: "standard", Invert: true}
	ramp := []rune(resolveCharset("standard"))
	img := &image.RGBA{
		Pix:    []byte{255, 255, 255, 255},
		Stride: 4, Rect: image.Rect(0, 0, 1, 1),
	}
	out := []rune(string(frameToAscii(img, buildRampLUT(ramp, opts))))
	if out[0] != ramp[0] {
		t.Errorf("inverted bright = %q, want %q", out[0], ramp[0])
	}
}

func TestRenderConcurrentSameKey(t *testing.T) {
	var jpegBuf bytes.Buffer
	src := image.NewRGBA(image.Rect(0, 0, 16, 16))
	for i := range src.Pix {
		src.Pix[i] = byte(i * 7)
	}
	if err := jpeg.Encode(&jpegBuf, src, nil); err != nil {
		t.Fatal(err)
	}

	r := NewRenderer(0, [][]byte{jpegBuf.Bytes()}, Options{
		BrightnessThreshold: 40,
		Charset:             "standard",
	}, NewCache(1<<20, false))

	var wg sync.WaitGroup
	results := make([][]byte, 32)
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ascii, err := r.Render(0, 20, 10, false, ColorTierTrueColor)
			if err != nil {
				t.Error(err)
				return
			}
			results[i] = ascii
		}(i)
	}
	wg.Wait()
	for i, got := range results {
		if !bytes.Equal(got, results[0]) {
			t.Fatalf("result %d differs from result 0", i)
		}
	}
}

func incompressible(n int) []byte {
	b := make([]byte, n)
	x := uint32(0x9e3779b9)
	for i := range b {
		x ^= x << 13
		x ^= x >> 17
		x ^= x << 5
		b[i] = byte(x)
	}
	return b
}

func TestRenderCacheEvictsByBytes(t *testing.T) {
	key := func(index int) cacheKey { return cacheKey{index: index} }
	payload := incompressible(1000)
	budget := 3 * entryCost(key(0), compressAscii(payload))
	c := NewCache(budget, true)
	for i := range 5 {
		c.put(key(i), payload)
	}
	if c.size.Load() > budget {
		t.Errorf("size %d exceeds budget %d", c.size.Load(), budget)
	}
	if _, ok := c.get(key(0)); ok {
		t.Error("oldest entry should have been evicted")
	}
	if _, ok := c.get(key(4)); !ok {
		t.Error("newest entry should be cached")
	}
}

func TestRenderCacheRoundTrips(t *testing.T) {
	for _, compress := range []bool{false, true} {
		c := NewCache(1<<20, compress)
		want := incompressible(4096)
		c.put(cacheKey{}, want)
		got, ok := c.get(cacheKey{})
		if !ok {
			t.Fatalf("compress=%v: entry should be cached", compress)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("compress=%v: entry does not match original", compress)
		}
	}
}

func TestRenderCacheDisabled(t *testing.T) {
	c := NewCache(0, false)
	if c != nil {
		t.Fatal("zero budget should disable the cache")
	}
	c.put(cacheKey{}, []byte("v"))
	if _, ok := c.get(cacheKey{}); ok {
		t.Error("nil cache should never hit")
	}
}

func TestRenderCacheRejectsOversizedEntry(t *testing.T) {
	c := NewCache(256, true)
	c.put(cacheKey{}, incompressible(10_000))
	if _, ok := c.get(cacheKey{}); ok {
		t.Error("entry larger than budget should not be cached")
	}
	if c.size.Load() != 0 {
		t.Errorf("size = %d, want 0", c.size.Load())
	}
}

func TestRenderCacheAccountsCompressedSize(t *testing.T) {
	cache := NewCache(512, true)
	value := make([]byte, 1, 4096)
	cache.put(cacheKey{}, value)
	if _, ok := cache.get(cacheKey{}); !ok {
		t.Fatal("small value should be cached regardless of its backing capacity")
	}
	if got := cache.size.Load(); got > 512 {
		t.Fatalf("size = %d, want <= 512", got)
	}
}

func TestAnsi256CoalescesQuantizedColors(t *testing.T) {
	img := &image.RGBA{
		Pix:    []byte{96, 96, 96, 255, 100, 100, 100, 255},
		Stride: 8,
		Rect:   image.Rect(0, 0, 2, 1),
	}
	output := frameToAnsi(img, buildRampLUT([]rune(" .#"), Options{}), ColorTier256)
	if count := bytes.Count(output, []byte("\x1b[38;5;")); count != 1 {
		t.Fatalf("color escape count = %d, want 1: %q", count, output)
	}
}

func TestAnsiDoesNotResetEachRow(t *testing.T) {
	img := &image.RGBA{
		Pix:    []byte{100, 100, 100, 255, 100, 100, 100, 255},
		Stride: 4,
		Rect:   image.Rect(0, 0, 1, 2),
	}
	output := frameToAnsi(img, buildRampLUT([]rune(" .#"), Options{}), ColorTierTrueColor)
	if count := bytes.Count(output, []byte(ansiReset)); count != 1 {
		t.Fatalf("reset count = %d, want 1: %q", count, output)
	}
}

func TestDetectColorTier(t *testing.T) {
	cases := map[string]ColorTier{
		"":                ColorTierNone,
		"dumb":            ColorTierNone,
		"vt100":           ColorTierNone,
		"linux":           ColorTierNone,
		"xterm":           ColorTierTrueColor,
		"xterm-256color":  ColorTier256,
		"screen-256color": ColorTier256,
		"tmux-256color":   ColorTier256,
		"xterm-direct":    ColorTierTrueColor,
		"xterm-kitty":     ColorTierTrueColor,
	}
	for term, want := range cases {
		if got := DetectColorTier(term); got != want {
			t.Errorf("DetectColorTier(%q) = %d, want %d", term, got, want)
		}
	}
}

func TestQuantize256(t *testing.T) {
	if got := quantize256(0, 0, 0); got != 16 {
		t.Errorf("black = %d, want 16", got)
	}
	if got := quantize256(255, 255, 255); got != 231 {
		t.Errorf("white = %d, want 231", got)
	}
}
