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
	Frames [][]byte
	FPS    float64
	Name   string
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

func resizeFrame(frame []byte, width, height int, keepAspectRatio bool) ([]byte, error) {
	src, err := jpeg.Decode(bytes.NewReader(frame))
	if err != nil {
		return nil, err
	}
	dst := image.NewGray(image.Rect(0, 0, width, height))
	if keepAspectRatio {
		draw.Draw(dst, dst.Bounds(), image.NewUniform(color.Gray{0}), image.Point{}, draw.Src)
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
	return dst.Pix, nil
}

type asciiOptions struct {
	brightnessThreshold int
	charset             string
	invert              bool
}

func frameToAscii(pixels []byte, options asciiOptions) string {
	ramp := []rune(resolveCharset(options.charset))
	total := len(ramp)
	var b strings.Builder
	for _, p := range pixels {
		brightness := int(p) * 100 / 255
		var index int
		if brightness < options.brightnessThreshold {
			index = 0
		} else {
			index = brightness * total / 100
			if index > total-1 {
				index = total - 1
			}
		}
		if options.invert {
			index = total - 1 - index
		}
		b.WriteRune(ramp[index])
	}
	return b.String()
}

type FrameRenderer struct {
	frames     [][]byte
	options    asciiOptions
	maxEntries int

	mu    sync.Mutex
	cache map[string]*list.Element
	order *list.List
}

type cacheEntry struct {
	key   string
	ascii string
}

func newFrameRenderer(frames [][]byte, options asciiOptions) *FrameRenderer {
	return &FrameRenderer{
		frames:     frames,
		options:    options,
		maxEntries: 4096,
		cache:      make(map[string]*list.Element),
		order:      list.New(),
	}
}

func (r *FrameRenderer) render(index, width, height int, keepAspectRatio bool) (string, error) {
	key := fmt.Sprintf("%d:%dx%d:%t", index, width, height, keepAspectRatio)

	r.mu.Lock()
	if el, ok := r.cache[key]; ok {
		r.order.MoveToBack(el)
		ascii := el.Value.(*cacheEntry).ascii
		r.mu.Unlock()
		return ascii, nil
	}
	r.mu.Unlock()

	pixels, err := resizeFrame(r.frames[index], width, height, keepAspectRatio)
	if err != nil {
		return "", err
	}
	ascii := frameToAscii(pixels, r.options)

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
