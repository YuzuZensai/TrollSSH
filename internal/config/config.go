package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/YuzuZensai/TrollSSH/internal/logx"
)

type PlaybackMode string

const (
	PlaybackLoop   PlaybackMode = "loop"
	PlaybackRandom PlaybackMode = "random"
)

type Config struct {
	Host                string
	Port                int
	MaxLoop             int
	PlaybackMode        PlaybackMode
	AllowUserControl    bool
	SwitchDebounce      time.Duration
	LoginDelay          time.Duration
	MaxConnections      int
	MaxTotalConnections int
	MaxAuthAttempts     int
	HandshakeTimeout    time.Duration
	MaxDimension        int
	MaxTerminalCells    int
	SessionTimeout      time.Duration
	RenderCacheMB       int
	BrightnessThreshold int
	Charset             string
	Invert              bool
	ForceGrayscale      bool
	LogCredentials      bool
}

func warnInvalid(name, value string, fallback any) {
	logx.Warn(fmt.Sprintf("Invalid %s=%q, using default %v", name, logx.Sanitize(value), fallback))
}

func envString(name, fallback string) string {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	return value
}

func envInt(name string, fallback, min, max int) int {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(raw)
	if err != nil {
		warnInvalid(name, raw, fallback)
		return fallback
	}
	if parsed < min {
		return min
	}
	if parsed > max {
		return max
	}
	return parsed
}

func envBool(name string, fallback bool) bool {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(strings.ToLower(raw))
	if err != nil {
		warnInvalid(name, raw, fallback)
		return fallback
	}
	return parsed
}

func envDurationMs(name string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	ms, err := strconv.Atoi(raw)
	if err != nil {
		warnInvalid(name, raw, fallback)
		return fallback
	}
	if ms < 0 {
		ms = 0
	}
	return time.Duration(ms) * time.Millisecond
}

func envPlaybackMode(name string, fallback PlaybackMode) PlaybackMode {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return fallback
	}
	switch PlaybackMode(strings.ToLower(raw)) {
	case PlaybackLoop:
		return PlaybackLoop
	case PlaybackRandom:
		return PlaybackRandom
	}
	warnInvalid(name, raw, fallback)
	return fallback
}

func Load() Config {
	const maxInt = int(^uint(0) >> 1)
	return Config{
		Host:                envString("HOST", "0.0.0.0"),
		Port:                envInt("PORT", 22, 1, 65535),
		MaxLoop:             envInt("MAX_LOOP", 5, 0, maxInt),
		PlaybackMode:        envPlaybackMode("PLAYBACK_MODE", PlaybackLoop),
		AllowUserControl:    envBool("ALLOW_USER_CONTROL", true),
		SwitchDebounce:      envDurationMs("SWITCH_DEBOUNCE_MS", 120*time.Millisecond),
		LoginDelay:          envDurationMs("LOGIN_DELAY", 1500*time.Millisecond),
		MaxConnections:      envInt("MAX_CONNECTIONS", 10, 1, maxInt),
		MaxTotalConnections: envInt("MAX_TOTAL_CONNECTIONS", 1000, 1, maxInt),
		MaxAuthAttempts:     envInt("MAX_AUTH_ATTEMPTS", 6, 1, maxInt),
		HandshakeTimeout:    envDurationMs("HANDSHAKE_TIMEOUT", 10*time.Second),
		MaxDimension:        envInt("MAX_DIMENSION", 512, 1, 4096),
		MaxTerminalCells:    envInt("MAX_TERMINAL_CELLS", 500*512, 1, maxInt),
		SessionTimeout:      envDurationMs("SESSION_TIMEOUT", 10*time.Minute),
		RenderCacheMB:       envInt("RENDER_CACHE_MB", 256, 0, maxInt),
		BrightnessThreshold: envInt("BRIGHTNESS_THRESHOLD", 40, 0, 100),
		Charset:             envString("CHARSET", "detailed"),
		Invert:              envBool("INVERT", false),
		ForceGrayscale:      envBool("FORCE_GRAYSCALE", false),
		LogCredentials:      envBool("LOG_CREDENTIALS", false),
	}
}

func LoadOptionalTextFile(filePath string) (string, bool) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", false
	}
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	text = strings.ReplaceAll(text, "\n", "\r\n")
	return text, true
}
