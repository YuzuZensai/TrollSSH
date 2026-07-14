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

func resizeFrame(frame, pix []byte, width, height int, keepAspectRatio bool) (*image.RGBA, error) {
	src, err := jpeg.Decode(bytes.NewReader(frame))
	if err != nil {
		return nil, err
	}
	rect := image.Rect(0, 0, width, height)

	dst := &image.RGBA{Pix: pix[:4*width*height], Stride: 4 * width, Rect: rect}
	if keepAspectRatio {
		draw.Draw(dst, dst.Bounds(), image.NewUniform(color.Black), image.Point{}, draw.Src)
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

func buildRampLUT(ramp []rune, options asciiOptions) *[101][]byte {
	var lut [101][]byte
	for b := range lut {
		index := rampIndex(b, options.brightnessThreshold, len(ramp), options.invert)
		lut[b] = utf8.AppendRune(nil, ramp[index])
	}
	return &lut
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

func frameToAscii(img *image.RGBA, rampLUT *[101][]byte) string {
	pix := img.Pix
	buf := getOutBuf(len(pix))
	for o := 0; o < len(pix); o += 4 {
		brightness := (int(pix[o])*299 + int(pix[o+1])*587 + int(pix[o+2])*114) / 255 / 10
		buf = append(buf, rampLUT[brightness]...)
	}
	ascii := string(buf)
	putOutBuf(buf)
	return ascii
}

const ansiReset = "\x1b[0m"

var ansi256Levels = [6]int{0, 95, 135, 175, 215, 255}

var decimal = func() (t [256]string) {
	for i := range t {
		t[i] = strconv.Itoa(i)
	}
	return
}()

var ansi256Cube = func() (t [256]uint8) {
	for v := range t {
		best, bestDist := 0, 1<<30
		for i, l := range ansi256Levels {
			d := v - l
			if d < 0 {
				d = -d
			}
			if d < bestDist {
				bestDist, best = d, i
			}
		}
		t[v] = uint8(best)
	}
	return
}()

func quantize256(r, g, b uint8) int {
	return 16 + 36*int(ansi256Cube[r]) + 6*int(ansi256Cube[g]) + int(ansi256Cube[b])
}

func appendColor(buf []byte, r, g, b uint8, tier colorTier) []byte {
	if tier == colorTierTrueColor {
		buf = append(buf, "\x1b[38;2;"...)
		buf = append(buf, decimal[r]...)
		buf = append(buf, ';')
		buf = append(buf, decimal[g]...)
		buf = append(buf, ';')
		buf = append(buf, decimal[b]...)
	} else {
		buf = append(buf, "\x1b[38;5;"...)
		buf = append(buf, decimal[quantize256(r, g, b)]...)
	}
	return append(buf, 'm')
}

func frameToAnsi(img *image.RGBA, rampLUT *[101][]byte, tier colorTier) string {
	bounds := img.Bounds()
	buf := getOutBuf(bounds.Dx() * bounds.Dy() * 16)
	var lastR, lastG, lastB uint8
	first := true
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			o := img.PixOffset(x, y)
			r, g, bl := img.Pix[o], img.Pix[o+1], img.Pix[o+2]
			brightness := (int(r)*299 + int(g)*587 + int(bl)*114) / 255 / 10
			if first || r != lastR || g != lastG || bl != lastB {
				buf = appendColor(buf, r, g, bl, tier)
				lastR, lastG, lastB = r, g, bl
				first = false
			}
			buf = append(buf, rampLUT[brightness]...)
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
	rampLUT     *[101][]byte
	cache       *renderCache

	inflightMu sync.Mutex
	inflight   map[string]chan struct{}
}

func newFrameRenderer(setID int, colorFrames [][]byte, options asciiOptions, cache *renderCache) *FrameRenderer {
	ramp := []rune(resolveCharset(options.charset))
	return &FrameRenderer{
		setID:       setID,
		colorFrames: colorFrames,
		options:     options,
		rampLUT:     buildRampLUT(ramp, options),
		cache:       cache,
		inflight:    make(map[string]chan struct{}),
	}
}

func (r *FrameRenderer) render(index, width, height int, keepAspectRatio bool, tier colorTier) (string, error) {
	key := fmt.Sprintf("%d:%d:%dx%d:%t:%d", r.setID, index, width, height, keepAspectRatio, tier)
	if ascii, ok := r.cache.get(key); ok {
		return ascii, nil
	}

	if r.cache != nil {
		for {
			r.inflightMu.Lock()
			wait, ok := r.inflight[key]
			if !ok {
				done := make(chan struct{})
				r.inflight[key] = done
				r.inflightMu.Unlock()
				defer func() {
					r.inflightMu.Lock()
					delete(r.inflight, key)
					r.inflightMu.Unlock()
					close(done)
				}()
				break
			}
			r.inflightMu.Unlock()
			<-wait
			if ascii, ok := r.cache.get(key); ok {
				return ascii, nil
			}
		}
	}

	pix := getPixBuf(4 * width * height)
	img, err := resizeFrame(r.colorFrames[index], pix, width, height, keepAspectRatio)
	if err != nil {
		putPixBuf(pix)
		return "", err
	}
	var ascii string
	if tier == colorTierNone {
		ascii = frameToAscii(img, r.rampLUT)
	} else {
		ascii = frameToAnsi(img, r.rampLUT, tier)
	}
	putPixBuf(pix)

	r.cache.put(key, ascii)
	return ascii, nil
}
