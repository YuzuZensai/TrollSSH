package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTSFRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "f.tsf")
	original := &FramesContainer{
		Frames: [][]byte{{0, 1, 255}, {10, 20, 30}},
		FPS:    29.97,
	}
	if err := writeTSF(path, original); err != nil {
		t.Fatalf("writeTSF: %v", err)
	}
	fc, err := loadTSF(path)
	if err != nil {
		t.Fatalf("loadTSF: %v", err)
	}
	if fc.FPS != 29.97 {
		t.Errorf("fps = %v", fc.FPS)
	}
	if len(fc.Frames) != 2 {
		t.Fatalf("frames = %d", len(fc.Frames))
	}
	if string(fc.Frames[0]) != string([]byte{0, 1, 255}) {
		t.Errorf("frame0 = %v", fc.Frames[0])
	}
	if string(fc.Frames[1]) != string([]byte{10, 20, 30}) {
		t.Errorf("frame1 = %v", fc.Frames[1])
	}
}

func TestTSFInvalid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.tsf")

	os.WriteFile(path, []byte("not a tsf file"), 0o644)
	if _, err := loadTSF(path); err == nil {
		t.Error("expected error for garbage input")
	}

	// Valid container but no frames.
	writeTSF(path, &FramesContainer{FPS: 30})
	if _, err := loadTSF(path); err == nil {
		t.Error("expected error for empty frames")
	}

	// Valid container but fps <= 0.
	writeTSF(path, &FramesContainer{Frames: [][]byte{{1}}, FPS: 0})
	if _, err := loadTSF(path); err == nil {
		t.Error("expected error for fps<=0")
	}

	// Truncated payload.
	writeTSF(path, &FramesContainer{Frames: [][]byte{{1, 2, 3, 4}}, FPS: 30})
	raw, _ := os.ReadFile(path)
	os.WriteFile(path, raw[:len(raw)-2], 0o644)
	if _, err := loadTSF(path); err == nil {
		t.Error("expected error for truncated file")
	}
}

func TestLoadConfigDefaults(t *testing.T) {
	t.Setenv("HOST", "")
	t.Setenv("PORT", "")
	t.Setenv("PLAYBACK_MODE", "")
	t.Setenv("LOGIN_DELAY", "")
	cfg := loadConfig()
	if cfg.Host != "0.0.0.0" {
		t.Errorf("host = %q", cfg.Host)
	}
	if cfg.Port != 22 {
		t.Errorf("port = %d", cfg.Port)
	}
	if cfg.PlaybackMode != PlaybackLoop {
		t.Errorf("playbackMode = %q", cfg.PlaybackMode)
	}
	if cfg.Charset != "detailed" {
		t.Errorf("charset = %q", cfg.Charset)
	}
	if cfg.LoginDelay != 1500*time.Millisecond {
		t.Errorf("loginDelay = %v", cfg.LoginDelay)
	}
}

func TestLoadConfigClamping(t *testing.T) {
	t.Setenv("PORT", "999999")
	t.Setenv("BRIGHTNESS_THRESHOLD", "-5")
	cfg := loadConfig()
	if cfg.Port != 65535 {
		t.Errorf("port clamp = %d", cfg.Port)
	}
	if cfg.BrightnessThreshold != 0 {
		t.Errorf("brightness clamp = %d", cfg.BrightnessThreshold)
	}
}

func TestLoadConfigInvalidFallsBack(t *testing.T) {
	t.Setenv("PORT", "not-a-number")
	t.Setenv("INVERT", "yes-please")
	t.Setenv("PLAYBACK_MODE", "shuffle")
	cfg := loadConfig()
	if cfg.Port != 22 {
		t.Errorf("port = %d, want default 22", cfg.Port)
	}
	if cfg.Invert {
		t.Error("invert should fall back to false")
	}
	if cfg.PlaybackMode != PlaybackLoop {
		t.Errorf("playbackMode = %q, want default loop", cfg.PlaybackMode)
	}
}

func TestEnvDurationMs(t *testing.T) {
	t.Setenv("D", "250")
	if got := envDurationMs("D", time.Second); got != 250*time.Millisecond {
		t.Errorf("250 = %v, want 250ms", got)
	}
	t.Setenv("D", "-10")
	if got := envDurationMs("D", time.Second); got != 0 {
		t.Errorf("negative = %v, want 0", got)
	}
	t.Setenv("D", "banana")
	if got := envDurationMs("D", time.Second); got != time.Second {
		t.Errorf("invalid = %v, want fallback 1s", got)
	}
}

func TestPlaybackModeRandom(t *testing.T) {
	t.Setenv("PLAYBACK_MODE", "RaNdOm")
	if loadConfig().PlaybackMode != PlaybackRandom {
		t.Error("expected random")
	}
}

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
	opts := asciiOptions{brightnessThreshold: 40, charset: "standard"}
	ramp := []rune(resolveCharset("standard"))
	out := []rune(frameToAscii([]byte{0, 255}, opts))
	if out[0] != ramp[0] {
		t.Errorf("dark px = %q, want %q", out[0], ramp[0])
	}
	if out[1] != ramp[len(ramp)-1] {
		t.Errorf("bright px = %q, want %q", out[1], ramp[len(ramp)-1])
	}
}

func TestFrameToAsciiInvert(t *testing.T) {
	opts := asciiOptions{brightnessThreshold: 40, charset: "standard", invert: true}
	ramp := []rune(resolveCharset("standard"))
	out := []rune(frameToAscii([]byte{255}, opts))
	if out[0] != ramp[0] {
		t.Errorf("inverted bright = %q, want %q", out[0], ramp[0])
	}
}

func TestConnectionTracker(t *testing.T) {
	tr := newConnectionTracker()
	tr.increment("1.2.3.4")
	tr.increment("1.2.3.4")
	if !tr.hasReachedLimits("1.2.3.4", 2, 100) {
		t.Error("expected per-ip limit reached")
	}
	tr.decrement("1.2.3.4")
	tr.decrement("1.2.3.4")
	if tr.totalCount() != 0 {
		t.Errorf("total = %d", tr.totalCount())
	}
	if tr.hasReachedLimits("1.2.3.4", 2, 100) {
		t.Error("should be cleared")
	}
}

func TestClampDimension(t *testing.T) {
	if clampDimension(0, 100) != 1 {
		t.Error("floor")
	}
	if clampDimension(500, 100) != 100 {
		t.Error("ceil")
	}
	if clampDimension(50, 100) != 50 {
		t.Error("passthrough")
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
}
