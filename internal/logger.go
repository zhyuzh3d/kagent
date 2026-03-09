package app

import (
	"fmt"
	"os"
	"runtime"
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

// InitLogger initializes the global structured logger.
func InitLogger(level LogLevel) {
	currentLevel = level
}

// mapFilename translates raw Go filenames to standardized Semantic Modules.
func mapFilename(file string) string {
	switch file {
	case "session.go":
		return "SessionManager"
	case "pipeline.go":
		return "AudioPipeline"
	case "asr.go":
		return "ASRBackend"
	case "main.go":
		return "System"
	default:
		return file
	}
}

// formatLog constructs the string:
// 2026-03-08 10:45:12 [INFO] [SessionManager:210] ...message...
func formatLog(level LogLevel, levelStr string, format string, args ...any) {
	if level < currentLevel {
		return
	}

	// Capture caller
	var pcs [1]uintptr
	runtime.Callers(3, pcs[:])
	frame, _ := runtime.CallersFrames(pcs[:]).Next()

	// Extract just the filename from the path
	file := frame.File
	if lastSlash := strings.LastIndexByte(file, '/'); lastSlash >= 0 {
		file = file[lastSlash+1:]
	}
	semanticModule := mapFilename(file)

	msg := format
	if len(args) > 0 {
		msg = fmt.Sprintf(format, args...)
	}

	ts := time.Now().Format("2006-01-02 15:04:05.000")
	fmt.Fprintf(os.Stdout, "%s [%s] [%s:%d] %s\n", ts, levelStr, semanticModule, frame.Line, msg)
}

// Debugf logs at LevelDebug
func Debugf(format string, args ...any) {
	formatLog(LevelDebug, "DEBUG", format, args...)
}

// Infof logs at LevelInfo
func Infof(format string, args ...any) {
	formatLog(LevelInfo, "INFO", format, args...)
}

// Warnf logs at LevelWarn
func Warnf(format string, args ...any) {
	formatLog(LevelWarn, "WARN", format, args...)
}

// Errorf logs at LevelError
func Errorf(format string, args ...any) {
	formatLog(LevelError, "ERROR", format, args...)
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
