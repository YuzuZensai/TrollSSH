package render

import (
	"bytes"
	"compress/flate"
	"container/list"
	"io"
	"sync"
	"sync/atomic"
	"time"
)

type cacheKey struct {
	setID           int
	index           int
	width           int
	height          int
	keepAspectRatio bool
	tier            ColorTier
}

type cacheShard struct {
	mu       sync.Mutex
	maxBytes int64
	size     int64
	entries  map[cacheKey]*list.Element
	order    *list.List
}

type Cache struct {
	shards     []cacheShard
	compress   bool
	size       atomic.Int64
	hits       atomic.Uint64
	misses     atomic.Uint64
	evictions  atomic.Uint64
	rejections atomic.Uint64
	renders    atomic.Uint64
	renderNs   atomic.Uint64
}

type cacheEntry struct {
	key     cacheKey
	data    []byte
	origLen int
	cost    int64
}

func entryCost(_ cacheKey, data []byte) int64 {
	return int64(cap(data)) + 160
}

var flateWriters = sync.Pool{New: func() any {
	w, _ := flate.NewWriter(io.Discard, 1)
	return w
}}

type flateReader interface {
	io.Reader
	flate.Resetter
}

var flateReaders = sync.Pool{New: func() any {
	return flate.NewReader(bytes.NewReader(nil)).(flateReader)
}}

func compressAscii(src []byte) []byte {
	var buf bytes.Buffer
	buf.Grow(len(src)/3 + 64)
	w := flateWriters.Get().(*flate.Writer)
	w.Reset(&buf)
	_, _ = w.Write(src)
	_ = w.Close()
	flateWriters.Put(w)
	return bytes.Clone(buf.Bytes())
}

func decompressAscii(src []byte, origLen int) []byte {
	r := flateReaders.Get().(flateReader)
	_ = r.Reset(bytes.NewReader(src), nil)
	buf := bytes.NewBuffer(make([]byte, 0, origLen))
	_, _ = io.Copy(buf, r)
	flateReaders.Put(r)
	return buf.Bytes()
}

func NewCache(maxBytes int64, compress bool) *Cache {
	if maxBytes <= 0 {
		return nil
	}
	shardCount := int(min(int64(16), max(int64(1), maxBytes/(1<<20))))
	cache := &Cache{shards: make([]cacheShard, shardCount), compress: compress}
	for i := range cache.shards {
		cache.shards[i] = cacheShard{
			maxBytes: maxBytes / int64(shardCount),
			entries:  make(map[cacheKey]*list.Element),
			order:    list.New(),
		}
	}
	return cache
}

func (c *Cache) shard(key cacheKey) *cacheShard {
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

func (c *Cache) get(key cacheKey) ([]byte, bool) {
	if c == nil {
		return nil, false
	}
	shard := c.shard(key)
	shard.mu.Lock()
	el, ok := shard.entries[key]
	if !ok {
		shard.mu.Unlock()
		c.misses.Add(1)
		return nil, false
	}
	shard.order.MoveToBack(el)
	entry := el.Value.(*cacheEntry)
	data, origLen := entry.data, entry.origLen
	shard.mu.Unlock()
	c.hits.Add(1)
	if !c.compress {
		return data, true
	}
	return decompressAscii(data, origLen), true
}

func (c *Cache) put(key cacheKey, ascii []byte) {
	if c == nil {
		return
	}
	shard := c.shard(key)
	data, origLen := ascii, 0
	if c.compress {
		data, origLen = compressAscii(ascii), len(ascii)
	}
	cost := entryCost(key, data)
	if cost > shard.maxBytes {
		c.rejections.Add(1)
		return
	}
	shard.mu.Lock()
	defer shard.mu.Unlock()
	if _, ok := shard.entries[key]; ok {
		return
	}
	entry := &cacheEntry{key: key, data: data, origLen: origLen, cost: cost}
	shard.entries[key] = shard.order.PushBack(entry)
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

type CacheStats struct {
	SizeBytes  int64
	Hits       uint64
	Misses     uint64
	Evictions  uint64
	Rejections uint64
	Renders    uint64
	RenderTime time.Duration
}

func (c *Cache) Stats() CacheStats {
	if c == nil {
		return CacheStats{}
	}
	return CacheStats{
		SizeBytes:  c.size.Load(),
		Hits:       c.hits.Load(),
		Misses:     c.misses.Load(),
		Evictions:  c.evictions.Load(),
		Rejections: c.rejections.Load(),
		Renders:    c.renders.Load(),
		RenderTime: time.Duration(c.renderNs.Load()),
	}
}

type Renderer struct {
	setID       int
	colorFrames [][]byte
	options     Options
	rampLUT     *[101][]byte
	cache       *Cache

	inflightMu sync.Mutex
	inflight   map[cacheKey]*renderCall
}

type renderCall struct {
	done  chan struct{}
	value []byte
	err   error
}

func NewRenderer(setID int, colorFrames [][]byte, options Options, cache *Cache) *Renderer {
	ramp := []rune(resolveCharset(options.Charset))
	return &Renderer{
		setID:       setID,
		colorFrames: colorFrames,
		options:     options,
		rampLUT:     buildRampLUT(ramp, options),
		cache:       cache,
		inflight:    make(map[cacheKey]*renderCall),
	}
}

func (r *Renderer) Render(index, width, height int, keepAspectRatio bool, tier ColorTier) ([]byte, error) {
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
	if tier == ColorTierNone {
		ascii = frameToAscii(img, r.rampLUT)
	} else {
		ascii = frameToAnsi(img, r.rampLUT, tier)
	}
	putPixBuf(pix)

	r.cache.put(key, ascii)
	if r.cache != nil {
		r.cache.renders.Add(1)
		r.cache.renderNs.Add(uint64(time.Since(started)))
	}
	call.value = ascii
	return ascii, nil
}
