package logx

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

type Level int

const (
	LevelDebug Level = 10
	LevelInfo  Level = 20
	LevelWarn  Level = 30
	LevelError Level = 40
)

var threshold = ResolveThreshold()

func ResolveThreshold() Level {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("LOG_LEVEL"))) {
	case "debug":
		return LevelDebug
	case "warn":
		return LevelWarn
	case "error":
		return LevelError
	default:
		return LevelInfo
	}
}

func SetThreshold(level Level) { threshold = level }

func Sanitize(value any) string {
	return SanitizeN(value, 200)
}

func SanitizeN(value any, maxLength int) string {
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
	count := 0
	for _, r := range str {
		if count >= maxLength {
			b.WriteRune('…')
			return b.String()
		}
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) {
			b.WriteRune('�')
		} else {
			b.WriteRune(r)
		}
		count++
	}
	return b.String()
}

func emit(level Level, name string, stream *os.File, args []any) {
	if level < threshold {
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
	_, _ = fmt.Fprintf(stream, "[%s] %-5s %s\n", ts, strings.ToUpper(name), strings.Join(parts, " "))
}

func Debug(args ...any) { emit(LevelDebug, "debug", os.Stdout, args) }
func Info(args ...any)  { emit(LevelInfo, "info", os.Stdout, args) }
func Warn(args ...any)  { emit(LevelWarn, "warn", os.Stderr, args) }
func Error(args ...any) { emit(LevelError, "error", os.Stderr, args) }
