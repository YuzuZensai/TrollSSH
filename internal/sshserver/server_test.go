package sshserver

import (
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"
)

func TestConnectionTracker(t *testing.T) {
	tr := newConnectionTracker()
	if _, _, ok := tr.tryAcquire("1.2.3.4", 2, 100); !ok {
		t.Fatal("first acquire failed")
	}
	if _, _, ok := tr.tryAcquire("1.2.3.4", 2, 100); !ok {
		t.Fatal("second acquire failed")
	}
	if _, _, ok := tr.tryAcquire("1.2.3.4", 2, 100); ok {
		t.Error("expected per-ip limit rejection")
	}
	tr.release("1.2.3.4")
	tr.release("1.2.3.4")
	if tr.totalCount() != 0 {
		t.Errorf("total = %d", tr.totalCount())
	}
	if _, _, ok := tr.tryAcquire("1.2.3.4", 2, 100); !ok {
		t.Error("limit should be cleared")
	}
}

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

func TestTermSizeAppliesFinalResizeAfterDebounce(t *testing.T) {
	size := &termSize{}
	size.set(80, 24, 512, 500*512, true)

	// Rapid burst of resize events, as happens during an interactive drag-resize.
	size.set(100, 40, 512, 500*512, false)
	size.set(150, 60, 512, 500*512, false)
	size.set(200, 100, 512, 500*512, false)

	if w, h := size.get(); w != 80 || h != 24 {
		t.Fatalf("size changed before debounce elapsed: %dx%d", w, h)
	}

	time.Sleep(resizeDebounce + 50*time.Millisecond)

	if w, h := size.get(); w != 200 || h != 100 {
		t.Fatalf("final resize was not applied after debounce: got %dx%d, want 200x100", w, h)
	}
}

func TestClampTermSize(t *testing.T) {
	w, h := clampTermSize(1000, 500, 512, 65536, 4)
	if w < 1 || h < 1 || w > 512 || h > 512 || w*h > 65536 {
		t.Fatalf("clamped size = %dx%d", w, h)
	}
	if w%4 != 0 || h%4 != 0 {
		t.Fatalf("size is not quantized: %dx%d", w, h)
	}
	w, h = clampTermSize(3, 2, 100, 100, 4)
	if w != 3 || h != 2 {
		t.Fatalf("small size = %dx%d", w, h)
	}
}

func TestParseDimsPtyReq(t *testing.T) {
	// "xterm" + cols=100 rows=40 + widthpx + heightpx
	payload := []byte{
		0, 0, 0, 5, 'x', 't', 'e', 'r', 'm',
		0, 0, 0, 100,
		0, 0, 0, 40,
		0, 0, 0, 0,
		0, 0, 0, 0,
	}
	cols, rows, ok := parseDims(payload)
	if !ok || cols != 100 || rows != 40 {
		t.Errorf("parseDims = %d,%d,%v", cols, rows, ok)
	}

	term, ok := parsePtyTerm(payload)
	if !ok || term != "xterm" {
		t.Errorf("parsePtyTerm = %q,%v", term, ok)
	}
}
