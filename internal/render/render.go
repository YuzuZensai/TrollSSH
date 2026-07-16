package render

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"strconv"
	"strings"
	"sync"
	"unicode/utf8"

	"golang.org/x/image/draw"
)

type ColorTier int

const (
	ColorTierNone ColorTier = iota
	ColorTier256
	ColorTierTrueColor
)

func DetectColorTier(term string) ColorTier {
	t := strings.ToLower(strings.TrimSpace(term))
	switch t {
	case "", "dumb", "vt52", "vt100", "vt102", "vt220", "ansi", "linux", "cons25", "cygwin":
		return ColorTierNone
	}
	if strings.Contains(t, "direct") || strings.Contains(t, "truecolor") {
		return ColorTierTrueColor
	}
	if strings.Contains(t, "256color") {
		return ColorTier256
	}
	if strings.HasPrefix(t, "screen") || strings.HasPrefix(t, "tmux") {
		return ColorTier256
	}
	return ColorTierTrueColor
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

const maxPooledBuffer = 4 << 20

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
	if cap(b) <= maxPooledBuffer {
		pixPool.Put(&b)
	}
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
	if cap(b) <= maxPooledBuffer {
		outPool.Put(&b)
	}
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

type Options struct {
	BrightnessThreshold int
	Charset             string
	Invert              bool
}

func buildRampLUT(ramp []rune, options Options) *[101][]byte {
	var lut [101][]byte
	for b := range lut {
		index := rampIndex(b, options.BrightnessThreshold, len(ramp), options.Invert)
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

func frameToAscii(img *image.RGBA, rampLUT *[101][]byte) []byte {
	pix := img.Pix
	maxCharBytes := 1
	for _, char := range rampLUT {
		maxCharBytes = max(maxCharBytes, len(char))
	}
	buf := getOutBuf(len(pix) / 4 * maxCharBytes)
	for o := 0; o < len(pix); o += 4 {
		brightness := (int(pix[o])*299 + int(pix[o+1])*587 + int(pix[o+2])*114) / 255 / 10
		buf = append(buf, rampLUT[brightness]...)
	}
	output := bytes.Clone(buf)
	putOutBuf(buf)
	return output
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

func appendColor(buf []byte, r, g, b uint8, tier ColorTier) []byte {
	if tier == ColorTierTrueColor {
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

func frameToAnsi(img *image.RGBA, rampLUT *[101][]byte, tier ColorTier) []byte {
	bounds := img.Bounds()
	bytesPerCell := 11
	if tier == ColorTierTrueColor {
		bytesPerCell = 16
	}
	buf := getOutBuf(bounds.Dx() * bounds.Dy() * bytesPerCell)
	var lastR, lastG, lastB uint8
	last256 := -1
	first := true
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		o := img.PixOffset(bounds.Min.X, y)
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			r, g, bl := img.Pix[o], img.Pix[o+1], img.Pix[o+2]
			brightness := (int(r)*299 + int(g)*587 + int(bl)*114) / 255 / 10
			colorChanged := first || r != lastR || g != lastG || bl != lastB
			if tier == ColorTier256 {
				index := quantize256(r, g, bl)
				colorChanged = first || index != last256
				last256 = index
			}
			if colorChanged {
				buf = appendColor(buf, r, g, bl, tier)
				lastR, lastG, lastB = r, g, bl
				first = false
			}
			buf = append(buf, rampLUT[brightness]...)
			o += 4
		}
		if y < bounds.Max.Y-1 {
			buf = append(buf, "\r\n"...)
		}
	}
	buf = append(buf, ansiReset...)
	output := bytes.Clone(buf)
	putOutBuf(buf)
	return output
}
