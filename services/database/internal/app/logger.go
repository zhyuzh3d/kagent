package app

import (
	"fmt"
	"os"
	"strings"
	"time"
)

type LogLevel int

const (
	LevelDebug LogLevel = iota
	LevelInfo
	LevelWarn
	LevelError
)

var currentLevel = LevelInfo
var currentTag = "APP"

// InitLogger initializes the global structured logger.
func InitLogger(level LogLevel, tag string) {
	currentLevel = level
	if strings.TrimSpace(tag) != "" {
		currentTag = strings.TrimSpace(tag)
	}
}

// formatLog constructs the string:
// 2026-03-08 10:45:12 [INFO] [SessionManager:210] ...message...
func formatLog(level LogLevel, levelStr string, tag string, format string, args ...any) {
	if level < currentLevel {
		return
	}

	useTag := currentTag
	if tag != "" {
		useTag = tag
	}

	// Capture caller (Optional: removed for extreme conciseness, can be re-added if needed)
	msg := format
	if len(args) > 0 {
		msg = fmt.Sprintf(format, args...)
	}

	ts := time.Now().Format("2006-01-02 15:04:05")
	// Standardized format: [YYYY-MM-DD HH:MM:SS] [LEVEL] [TAG] Message
	fmt.Fprintf(os.Stdout, "[%s] [%s] [%s] %s\n", ts, levelStr, useTag, msg)
}

// Debugf logs at LevelDebug
func Debugf(format string, args ...any) {
	formatLog(LevelDebug, "DEBUG", "", format, args...)
}

// Infof logs at LevelInfo
func Infof(format string, args ...any) {
	formatLog(LevelInfo, "INFO", "", format, args...)
}

// InfofTag logs at LevelInfo with a custom tag
func InfofTag(tag string, format string, args ...any) {
	formatLog(LevelInfo, "INFO", tag, format, args...)
}

// Succf logs at LevelInfo as SUCC
func Succf(format string, args ...any) {
	formatLog(LevelInfo, "SUCC", "", format, args...)
}

// SuccfTag logs at LevelInfo with a custom tag as SUCC
func SuccfTag(tag string, format string, args ...any) {
	formatLog(LevelInfo, "SUCC", tag, format, args...)
}

// Warnf logs at LevelWarn
func Warnf(format string, args ...any) {
	formatLog(LevelWarn, "WARN", "", format, args...)
}

// Errorf logs at LevelError
func Errorf(format string, args ...any) {
	formatLog(LevelError, "ERROR", "", format, args...)
}

// Snippet shortens a string to be suitable for logging contexts.
func Snippet(text string) string {
	text = strings.TrimSpace(text)
	r := []rune(text)
	if len(r) > 10 {
		return string(r[:10]) + "..."
	}
	return text
}
