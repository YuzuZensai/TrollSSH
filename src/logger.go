package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

type logLevel int

const (
	levelDebug logLevel = 10
	levelInfo  logLevel = 20
	levelWarn  logLevel = 30
	levelError logLevel = 40
)

var logThreshold = resolveThreshold()

func resolveThreshold() logLevel {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("LOG_LEVEL"))) {
	case "debug":
		return levelDebug
	case "warn":
		return levelWarn
	case "error":
		return levelError
	default:
		return levelInfo
	}
}

func sanitize(value any) string {
	return sanitizeN(value, 200)
}

func sanitizeN(value any, maxLength int) string {
	var str string
	switch v := value.(type) {
	case nil:
		str = ""
	case string:
		str = v
	default:
		str = fmt.Sprint(v)
	}
	var b strings.Builder
	for _, r := range str {
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			b.WriteRune('�')
		} else {
			b.WriteRune(r)
		}
	}
	out := []rune(b.String())
	if len(out) > maxLength {
		return string(out[:maxLength]) + "…"
	}
	return string(out)
}

func emit(level logLevel, name string, stream *os.File, args []any) {
	if level < logThreshold {
		return
	}
	parts := make([]string, len(args))
	for i, a := range args {
		if s, ok := a.(string); ok {
			parts[i] = s
		} else if b, err := json.Marshal(a); err == nil {
			parts[i] = string(b)
		} else {
			parts[i] = fmt.Sprint(a)
		}
	}
	ts := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	fmt.Fprintf(stream, "[%s] %-5s %s\n", ts, strings.ToUpper(name), strings.Join(parts, " "))
}

func logDebug(args ...any) { emit(levelDebug, "debug", os.Stdout, args) }
func logInfo(args ...any)  { emit(levelInfo, "info", os.Stdout, args) }
func logWarn(args ...any)  { emit(levelWarn, "warn", os.Stderr, args) }
func logError(args ...any) { emit(levelError, "error", os.Stderr, args) }
