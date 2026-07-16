package main

import (
	"bytes"
	"image"
	"sync"
	"testing"

	"golang.org/x/crypto/ssh"
)

func TestConnectionTrackerConcurrentLimit(t *testing.T) {
	tracker := newConnectionTracker()
	start := make(chan struct{})
	var wg sync.WaitGroup
	var mu sync.Mutex
	accepted := make(map[string]int)
	for i := range 100 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			ip := string(rune('a' + i%10))
			if _, _, ok := tracker.tryAcquire(ip, 3, 7); ok {
				mu.Lock()
				accepted[ip]++
				mu.Unlock()
			}
		}(i)
	}
	close(start)
	wg.Wait()

	total := 0
	for ip, count := range accepted {
		total += count
		if count > 3 {
			t.Fatalf("IP %q acquired %d slots", ip, count)
		}
	}
	if total != 7 || tracker.totalCount() != 7 {
		t.Fatalf("accepted=%d tracked=%d, want 7", total, tracker.totalCount())
	}
	for ip, count := range accepted {
		for range count {
			tracker.release(ip)
		}
	}
}

func TestSessionTrackerLimits(t *testing.T) {
	tracker := newSessionTracker()
	first := &ssh.ServerConn{}
	second := &ssh.ServerConn{}
	if !tracker.tryAcquire(first, 1, 2) {
		t.Fatal("first session rejected")
	}
	if tracker.tryAcquire(first, 1, 2) {
		t.Fatal("per-connection limit was not enforced")
	}
	if !tracker.tryAcquire(second, 1, 2) {
		t.Fatal("second connection session rejected")
	}
	if tracker.tryAcquire(&ssh.ServerConn{}, 1, 2) {
		t.Fatal("global session limit was not enforced")
	}
	tracker.release(first)
	if !tracker.tryAcquire(&ssh.ServerConn{}, 1, 2) {
		t.Fatal("released slot was not reusable")
	}
}

func TestTermSizeDebouncesResize(t *testing.T) {
	size := &termSize{}
	size.set(80, 24, 512, 500*512, true)
	size.set(200, 100, 512, 500*512, false)
	if w, h := size.get(); w != 80 || h != 24 {
		t.Fatalf("debounced size = %dx%d", w, h)
	}
	size.set(200, 100, 512, 500*512, true)
	if w, h := size.get(); w != 200 || h != 100 {
		t.Fatalf("forced size = %dx%d", w, h)
	}
}

func TestAnsi256CoalescesQuantizedColors(t *testing.T) {
	img := &image.RGBA{
		Pix:    []byte{96, 96, 96, 255, 100, 100, 100, 255},
		Stride: 8,
		Rect:   image.Rect(0, 0, 2, 1),
	}
	output := frameToAnsi(img, buildRampLUT([]rune(" .#"), asciiOptions{}), colorTier256)
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
	output := frameToAnsi(img, buildRampLUT([]rune(" .#"), asciiOptions{}), colorTierTrueColor)
	if count := bytes.Count(output, []byte(ansiReset)); count != 1 {
		t.Fatalf("reset count = %d, want 1: %q", count, output)
	}
}

func TestRenderCacheAccountsRetainedCapacity(t *testing.T) {
	cache := newRenderCache(512)
	value := make([]byte, 1, 4096)
	cache.put(cacheKey{}, value)
	if _, ok := cache.get(cacheKey{}); ok {
		t.Fatal("cache accepted an entry whose backing allocation exceeds its budget")
	}
	if cache.stats().Rejections != 1 {
		t.Fatalf("rejections = %d, want 1", cache.stats().Rejections)
	}
}

func TestSanitizeNStopsAtLimit(t *testing.T) {
	input := "ab\x00cdefghijklmnopqrstuvwxyz"
	if got := sanitizeN(input, 4); got != "ab�c…" {
		t.Fatalf("sanitizeN = %q", got)
	}
}
