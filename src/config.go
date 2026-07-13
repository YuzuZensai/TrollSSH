package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
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
	FrameResolution     int
	BrightnessThreshold int
	Charset             string
	Invert              bool
	LogCredentials      bool
}

func warnInvalid(name, value string, fallback any) {
	logWarn(fmt.Sprintf("Invalid %s=%q, using default %v", name, sanitize(value), fallback))
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

func loadConfig() Config {
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
		FrameResolution:     envInt("FRAME_RESOLUTION", 360, 16, 1080),
		BrightnessThreshold: envInt("BRIGHTNESS_THRESHOLD", 40, 0, 100),
		Charset:             envString("CHARSET", "detailed"),
		Invert:              envBool("INVERT", false),
		LogCredentials:      envBool("LOG_CREDENTIALS", false),
	}
}

func loadOptionalTextFile(filePath string) (string, bool) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", false
	}
	return string(data), true
}
