package main

import (
	"bytes"
	"container/list"
	"image"
	"image/color"
	"image/jpeg"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
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

func frameToAnsi(img *image.RGBA, rampLUT *[101][]byte, tier colorTier) []byte {
	bounds := img.Bounds()
	bytesPerCell := 11
	if tier == colorTierTrueColor {
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
			if tier == colorTier256 {
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

type cacheKey struct {
	setID           int
	index           int
	width           int
	height          int
	keepAspectRatio bool
	tier            colorTier
}

type renderCacheShard struct {
	mu       sync.Mutex
	maxBytes int64
	size     int64
	entries  map[cacheKey]*list.Element
	order    *list.List
}

type renderCache struct {
	shards     []renderCacheShard
	size       atomic.Int64
	hits       atomic.Uint64
	misses     atomic.Uint64
	evictions  atomic.Uint64
	rejections atomic.Uint64
	renders    atomic.Uint64
	renderNs   atomic.Uint64
}

type cacheEntry struct {
	key   cacheKey
	ascii []byte
	cost  int64
}

func entryCost(_ cacheKey, ascii []byte) int64 {
	return int64(cap(ascii)) + 160
}

func newRenderCache(maxBytes int64) *renderCache {
	if maxBytes <= 0 {
		return nil
	}
	shardCount := int(min(int64(16), max(int64(1), maxBytes/(1<<20))))
	cache := &renderCache{shards: make([]renderCacheShard, shardCount)}
	for i := range cache.shards {
		cache.shards[i] = renderCacheShard{
			maxBytes: maxBytes / int64(shardCount),
			entries:  make(map[cacheKey]*list.Element),
			order:    list.New(),
		}
	}
	return cache
}

func (c *renderCache) shard(key cacheKey) *renderCacheShard {
	hash := uint64(key.setID)*0x9e3779b185ebca87 ^ uint64(key.index)*0xc2b2ae3d27d4eb4f
	hash ^= uint64(key.width)<<32 | uint64(uint32(key.height))
	hash ^= uint64(key.tier)<<1 | uint64(boolToInt(key.keepAspectRatio))
	return &c.shards[hash%uint64(len(c.shards))]
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func (c *renderCache) get(key cacheKey) ([]byte, bool) {
	if c == nil {
		return nil, false
	}
	shard := c.shard(key)
	shard.mu.Lock()
	defer shard.mu.Unlock()
	el, ok := shard.entries[key]
	if !ok {
		c.misses.Add(1)
		return nil, false
	}
	c.hits.Add(1)
	shard.order.MoveToBack(el)
	return el.Value.(*cacheEntry).ascii, true
}

func (c *renderCache) put(key cacheKey, ascii []byte) {
	if c == nil {
		return
	}
	shard := c.shard(key)
	cost := entryCost(key, ascii)
	if cost > shard.maxBytes {
		c.rejections.Add(1)
		return
	}
	shard.mu.Lock()
	defer shard.mu.Unlock()
	if _, ok := shard.entries[key]; ok {
		return
	}
	shard.entries[key] = shard.order.PushBack(&cacheEntry{key: key, ascii: ascii, cost: cost})
	shard.size += cost
	c.size.Add(cost)
	for shard.size > shard.maxBytes {
		oldest := shard.order.Front()
		shard.order.Remove(oldest)
		evicted := oldest.Value.(*cacheEntry)
		delete(shard.entries, evicted.key)
		shard.size -= evicted.cost
		c.size.Add(-evicted.cost)
		c.evictions.Add(1)
	}
}

type renderCacheStats struct {
	SizeBytes  int64
	Hits       uint64
	Misses     uint64
	Evictions  uint64
	Rejections uint64
	Renders    uint64
	RenderTime time.Duration
}

func (c *renderCache) stats() renderCacheStats {
	if c == nil {
		return renderCacheStats{}
	}
	return renderCacheStats{
		SizeBytes:  c.size.Load(),
		Hits:       c.hits.Load(),
		Misses:     c.misses.Load(),
		Evictions:  c.evictions.Load(),
		Rejections: c.rejections.Load(),
		Renders:    c.renders.Load(),
		RenderTime: time.Duration(c.renderNs.Load()),
	}
}

type FrameRenderer struct {
	setID       int
	colorFrames [][]byte
	options     asciiOptions
	rampLUT     *[101][]byte
	cache       *renderCache

	inflightMu sync.Mutex
	inflight   map[cacheKey]*renderCall
}

type renderCall struct {
	done  chan struct{}
	value []byte
	err   error
}

func newFrameRenderer(setID int, colorFrames [][]byte, options asciiOptions, cache *renderCache) *FrameRenderer {
	ramp := []rune(resolveCharset(options.charset))
	return &FrameRenderer{
		setID:       setID,
		colorFrames: colorFrames,
		options:     options,
		rampLUT:     buildRampLUT(ramp, options),
		cache:       cache,
		inflight:    make(map[cacheKey]*renderCall),
	}
}

func (r *FrameRenderer) render(index, width, height int, keepAspectRatio bool, tier colorTier) ([]byte, error) {
	key := cacheKey{r.setID, index, width, height, keepAspectRatio, tier}
	if ascii, ok := r.cache.get(key); ok {
		return ascii, nil
	}

	r.inflightMu.Lock()
	if call, ok := r.inflight[key]; ok {
		r.inflightMu.Unlock()
		<-call.done
		return call.value, call.err
	}
	call := &renderCall{done: make(chan struct{})}
	r.inflight[key] = call
	r.inflightMu.Unlock()
	defer func() {
		r.inflightMu.Lock()
		delete(r.inflight, key)
		r.inflightMu.Unlock()
		close(call.done)
	}()

	if ascii, ok := r.cache.get(key); ok {
		call.value = ascii
		return ascii, nil
	}

	started := time.Now()
	pix := getPixBuf(4 * width * height)
	img, err := resizeFrame(r.colorFrames[index], pix, width, height, keepAspectRatio)
	if err != nil {
		putPixBuf(pix)
		call.err = err
		return nil, err
	}
	var ascii []byte
	if tier == colorTierNone {
		ascii = frameToAscii(img, r.rampLUT)
	} else {
		ascii = frameToAnsi(img, r.rampLUT, tier)
	}
	putPixBuf(pix)

	if r.cache != nil && cap(ascii) > len(ascii)+len(ascii)/4 {
		ascii = bytes.Clone(ascii)
	}
	r.cache.put(key, ascii)
	if r.cache != nil {
		r.cache.renders.Add(1)
		r.cache.renderNs.Add(uint64(time.Since(started)))
	}
	call.value = ascii
	return ascii, nil
}
