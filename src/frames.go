package main

import (
	"bytes"
	"container/list"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"strings"
	"sync"

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

func resizeFrame(frame []byte, width, height int, keepAspectRatio bool, tier colorTier) (draw.Image, error) {
	src, err := jpeg.Decode(bytes.NewReader(frame))
	if err != nil {
		return nil, err
	}
	var dst draw.Image
	var bg color.Color
	if tier == colorTierNone {
		dst = image.NewGray(image.Rect(0, 0, width, height))
		bg = color.Gray{0}
	} else {
		dst = image.NewNRGBA(image.Rect(0, 0, width, height))
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

func frameToAscii(pixels []byte, options asciiOptions) string {
	ramp := []rune(resolveCharset(options.charset))
	total := len(ramp)
	var b strings.Builder
	for _, p := range pixels {
		brightness := int(p) * 100 / 255
		index := rampIndex(brightness, options.brightnessThreshold, total, options.invert)
		b.WriteRune(ramp[index])
	}
	return b.String()
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

func frameToAnsi(img *image.NRGBA, options asciiOptions, tier colorTier) string {
	ramp := []rune(resolveCharset(options.charset))
	total := len(ramp)
	bounds := img.Bounds()
	var b strings.Builder
	var lastR, lastG, lastB uint8
	first := true
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			o := img.PixOffset(x, y)
			r, g, bl := img.Pix[o], img.Pix[o+1], img.Pix[o+2]
			brightness := (int(r)*299 + int(g)*587 + int(bl)*114) / 255 / 10
			index := rampIndex(brightness, options.brightnessThreshold, total, options.invert)
			if first || r != lastR || g != lastG || bl != lastB {
				if tier == colorTierTrueColor {
					fmt.Fprintf(&b, "\x1b[38;2;%d;%d;%dm", r, g, bl)
				} else {
					fmt.Fprintf(&b, "\x1b[38;5;%dm", quantize256(r, g, bl))
				}
				lastR, lastG, lastB = r, g, bl
				first = false
			}
			b.WriteRune(ramp[index])
		}
		if y < bounds.Max.Y-1 {
			b.WriteString(ansiReset + "\r\n")
			first = true
		}
	}
	b.WriteString(ansiReset)
	return b.String()
}

type FrameRenderer struct {
	colorFrames [][]byte
	options     asciiOptions
	maxEntries  int

	mu    sync.Mutex
	cache map[string]*list.Element
	order *list.List
}

type cacheEntry struct {
	key   string
	ascii string
}

func newFrameRenderer(colorFrames [][]byte, options asciiOptions) *FrameRenderer {
	return &FrameRenderer{
		colorFrames: colorFrames,
		options:     options,
		maxEntries:  4096,
		cache:       make(map[string]*list.Element),
		order:       list.New(),
	}
}

func (r *FrameRenderer) render(index, width, height int, keepAspectRatio bool, tier colorTier) (string, error) {
	key := fmt.Sprintf("%d:%dx%d:%t:%d", index, width, height, keepAspectRatio, tier)

	r.mu.Lock()
	if el, ok := r.cache[key]; ok {
		r.order.MoveToBack(el)
		ascii := el.Value.(*cacheEntry).ascii
		r.mu.Unlock()
		return ascii, nil
	}
	r.mu.Unlock()

	var ascii string
	if tier == colorTierNone {
		img, err := resizeFrame(r.colorFrames[index], width, height, keepAspectRatio, tier)
		if err != nil {
			return "", err
		}
		ascii = frameToAscii(img.(*image.Gray).Pix, r.options)
	} else {
		img, err := resizeFrame(r.colorFrames[index], width, height, keepAspectRatio, tier)
		if err != nil {
			return "", err
		}
		ascii = frameToAnsi(img.(*image.NRGBA), r.options, tier)
	}

	r.mu.Lock()
	if _, ok := r.cache[key]; !ok {
		r.cache[key] = r.order.PushBack(&cacheEntry{key, ascii})
		if r.order.Len() > r.maxEntries {
			oldest := r.order.Front()
			r.order.Remove(oldest)
			delete(r.cache, oldest.Value.(*cacheEntry).key)
		}
	}
	r.mu.Unlock()
	return ascii, nil
}
