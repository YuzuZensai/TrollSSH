package main

import (
	"bytes"
	"container/list"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/image/draw"
)

type FramesContainer struct {
	ColorFrames [][]byte
	FPS         float64
	Name        string
}

type colorTier int

const (
	colorTierNone colorTier = iota
	colorTier256
	colorTierTrueColor
)

func detectColorTier(term string) colorTier {
	t := strings.ToLower(strings.TrimSpace(term))
	switch t {
	case "", "dumb", "vt52", "vt100", "vt102", "vt220", "ansi", "linux", "cons25", "cygwin":
		return colorTierNone
	}
	if strings.Contains(t, "direct") || strings.Contains(t, "truecolor") {
		return colorTierTrueColor
	}
	if strings.Contains(t, "256color") {
		return colorTier256
	}
	if strings.HasPrefix(t, "screen") || strings.HasPrefix(t, "tmux") {
		return colorTier256
	}
	return colorTierTrueColor
}

var charsetPresets = map[string]string{
	"detailed": " .'`^\",:;Il!i><~+_-?][}{1)(|/tfjrxnuvczXYUJCLQ0OZmwqpdbkhao*#MW&8%B@$",
	"standard": " .:-=+*#%@",
	"simple":   " .:oO#@",
	"blocks":   " ░▒▓█",
}

func resolveCharset(charset string) string {
	if charset == "" {
		return charsetPresets["detailed"]
	}
	if preset, ok := charsetPresets[strings.ToLower(charset)]; ok {
		return preset
	}
	return charset
}

var pixPool sync.Pool

func getPixBuf(n int) []byte {
	if v := pixPool.Get(); v != nil {
		if b := *v.(*[]byte); cap(b) >= n {
			return b[:n]
		}
	}
	return make([]byte, n)
}

func putPixBuf(b []byte) {
	pixPool.Put(&b)
}

var outPool sync.Pool

func getOutBuf(capacity int) []byte {
	if v := outPool.Get(); v != nil {
		if b := *v.(*[]byte); cap(b) >= capacity {
			return b[:0]
		}
	}
	return make([]byte, 0, capacity)
}

func putOutBuf(b []byte) {
	outPool.Put(&b)
}

func resizeFrame(frame, pix []byte, width, height int, keepAspectRatio bool, tier colorTier) (draw.Image, error) {
	src, err := jpeg.Decode(bytes.NewReader(frame))
	if err != nil {
		return nil, err
	}
	rect := image.Rect(0, 0, width, height)
	var dst draw.Image
	var bg color.Color
	if tier == colorTierNone {
		dst = &image.Gray{Pix: pix[:width*height], Stride: width, Rect: rect}
		bg = color.Gray{0}
	} else {
		dst = &image.NRGBA{Pix: pix[:4*width*height], Stride: 4 * width, Rect: rect}
		bg = color.Black
	}
	if keepAspectRatio {
		draw.Draw(dst, dst.Bounds(), image.NewUniform(bg), image.Point{}, draw.Src)
		sb := src.Bounds()
		sw, sh := sb.Dx(), sb.Dy()
		scale := min(float64(width)/float64(sw), float64(height)/float64(sh))
		tw := max(1, int(float64(sw)*scale))
		th := max(1, int(float64(sh)*scale))
		x0 := (width - tw) / 2
		y0 := (height - th) / 2
		draw.ApproxBiLinear.Scale(dst, image.Rect(x0, y0, x0+tw, y0+th), src, sb, draw.Src, nil)
	} else {
		draw.ApproxBiLinear.Scale(dst, dst.Bounds(), src, src.Bounds(), draw.Src, nil)
	}
	return dst, nil
}

type asciiOptions struct {
	brightnessThreshold int
	charset             string
	invert              bool
}

func rampIndex(brightness, threshold, total int, invert bool) int {
	var index int
	if brightness < threshold {
		index = 0
	} else {
		index = brightness * total / 100
		if index > total-1 {
			index = total - 1
		}
	}
	if invert {
		index = total - 1 - index
	}
	return index
}

func frameToAscii(pixels []byte, ramp []rune, options asciiOptions) string {
	total := len(ramp)
	buf := getOutBuf(len(pixels) * 4)
	for _, p := range pixels {
		brightness := int(p) * 100 / 255
		index := rampIndex(brightness, options.brightnessThreshold, total, options.invert)
		buf = utf8.AppendRune(buf, ramp[index])
	}
	ascii := string(buf)
	putOutBuf(buf)
	return ascii
}

const ansiReset = "\x1b[0m"

var ansi256Levels = [6]int{0, 95, 135, 175, 215, 255}

func quantize256(r, g, b uint8) int {
	toLevel := func(v uint8) int {
		best, bestDist := 0, 1<<30
		for i, l := range ansi256Levels {
			d := int(v) - l
			if d < 0 {
				d = -d
			}
			if d < bestDist {
				bestDist, best = d, i
			}
		}
		return best
	}
	return 16 + 36*toLevel(r) + 6*toLevel(g) + toLevel(b)
}

func appendColor(buf []byte, r, g, b uint8, tier colorTier) []byte {
	if tier == colorTierTrueColor {
		buf = append(buf, "\x1b[38;2;"...)
		buf = strconv.AppendUint(buf, uint64(r), 10)
		buf = append(buf, ';')
		buf = strconv.AppendUint(buf, uint64(g), 10)
		buf = append(buf, ';')
		buf = strconv.AppendUint(buf, uint64(b), 10)
	} else {
		buf = append(buf, "\x1b[38;5;"...)
		buf = strconv.AppendUint(buf, uint64(quantize256(r, g, b)), 10)
	}
	return append(buf, 'm')
}

func frameToAnsi(img *image.NRGBA, ramp []rune, options asciiOptions, tier colorTier) string {
	total := len(ramp)
	bounds := img.Bounds()
	buf := getOutBuf(bounds.Dx() * bounds.Dy() * 16)
	var lastR, lastG, lastB uint8
	first := true
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			o := img.PixOffset(x, y)
			r, g, bl := img.Pix[o], img.Pix[o+1], img.Pix[o+2]
			brightness := (int(r)*299 + int(g)*587 + int(bl)*114) / 255 / 10
			index := rampIndex(brightness, options.brightnessThreshold, total, options.invert)
			if first || r != lastR || g != lastG || bl != lastB {
				buf = appendColor(buf, r, g, bl, tier)
				lastR, lastG, lastB = r, g, bl
				first = false
			}
			buf = utf8.AppendRune(buf, ramp[index])
		}
		if y < bounds.Max.Y-1 {
			buf = append(buf, ansiReset+"\r\n"...)
			first = true
		}
	}
	buf = append(buf, ansiReset...)
	ascii := string(buf)
	putOutBuf(buf)
	return ascii
}

type renderCache struct {
	mu       sync.Mutex
	maxBytes int64
	size     int64
	entries  map[string]*list.Element
	order    *list.List
}

type cacheEntry struct {
	key   string
	ascii string
}

func entryCost(key, ascii string) int64 {
	return int64(len(key)+len(ascii)) + 128
}

func newRenderCache(maxBytes int64) *renderCache {
	if maxBytes <= 0 {
		return nil
	}
	return &renderCache{
		maxBytes: maxBytes,
		entries:  make(map[string]*list.Element),
		order:    list.New(),
	}
}

func (c *renderCache) get(key string) (string, bool) {
	if c == nil {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	el, ok := c.entries[key]
	if !ok {
		return "", false
	}
	c.order.MoveToBack(el)
	return el.Value.(*cacheEntry).ascii, true
}

func (c *renderCache) put(key, ascii string) {
	if c == nil {
		return
	}
	cost := entryCost(key, ascii)
	if cost > c.maxBytes {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, ok := c.entries[key]; ok {
		return
	}
	c.entries[key] = c.order.PushBack(&cacheEntry{key, ascii})
	c.size += cost
	for c.size > c.maxBytes {
		oldest := c.order.Front()
		c.order.Remove(oldest)
		evicted := oldest.Value.(*cacheEntry)
		delete(c.entries, evicted.key)
		c.size -= entryCost(evicted.key, evicted.ascii)
	}
}

type FrameRenderer struct {
	setID       int
	colorFrames [][]byte
	options     asciiOptions
	ramp        []rune
	cache       *renderCache
}

func newFrameRenderer(setID int, colorFrames [][]byte, options asciiOptions, cache *renderCache) *FrameRenderer {
	return &FrameRenderer{
		setID:       setID,
		colorFrames: colorFrames,
		options:     options,
		ramp:        []rune(resolveCharset(options.charset)),
		cache:       cache,
	}
}

func (r *FrameRenderer) render(index, width, height int, keepAspectRatio bool, tier colorTier) (string, error) {
	key := fmt.Sprintf("%d:%d:%dx%d:%t:%d", r.setID, index, width, height, keepAspectRatio, tier)
	if ascii, ok := r.cache.get(key); ok {
		return ascii, nil
	}

	n := width * height
	if tier != colorTierNone {
		n *= 4
	}
	pix := getPixBuf(n)
	img, err := resizeFrame(r.colorFrames[index], pix, width, height, keepAspectRatio, tier)
	if err != nil {
		putPixBuf(pix)
		return "", err
	}
	var ascii string
	if tier == colorTierNone {
		ascii = frameToAscii(img.(*image.Gray).Pix, r.ramp, r.options)
	} else {
		ascii = frameToAnsi(img.(*image.NRGBA), r.ramp, r.options, tier)
	}
	putPixBuf(pix)

	r.cache.put(key, ascii)
	return ascii, nil
}
