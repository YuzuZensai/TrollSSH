package config

import (
	"testing"
	"time"
)

func TestLoadConfigDefaults(t *testing.T) {
	t.Setenv("HOST", "")
	t.Setenv("PORT", "")
	t.Setenv("PLAYBACK_MODE", "")
	t.Setenv("LOGIN_DELAY", "")
	cfg := Load()
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
	cfg := Load()
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
	cfg := Load()
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
	if Load().PlaybackMode != PlaybackRandom {
		t.Error("expected random")
	}
}
