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
var currentTag = "ACCOUNT"

func InitLogger(level LogLevel, tag string) {
	currentLevel = level
	if strings.TrimSpace(tag) != "" {
		currentTag = strings.TrimSpace(tag)
	}
}

func formatLog(level LogLevel, levelStr string, tag string, format string, args ...any) {
	if level < currentLevel {
		return
	}

	useTag := currentTag
	if strings.TrimSpace(tag) != "" {
		useTag = strings.TrimSpace(tag)
	}

	msg := format
	if len(args) > 0 {
		msg = fmt.Sprintf(format, args...)
	}

	ts := time.Now().Format("2006-01-02 15:04:05")
	fmt.Fprintf(os.Stdout, "[%s] [%s] [%s] %s\n", ts, levelStr, useTag, msg)
}

func Debugf(format string, args ...any) {
	formatLog(LevelDebug, "DEBUG", "", format, args...)
}

func Infof(format string, args ...any) {
	formatLog(LevelInfo, "INFO", "", format, args...)
}

func Warnf(format string, args ...any) {
	formatLog(LevelWarn, "WARN", "", format, args...)
}

func Errorf(format string, args ...any) {
	formatLog(LevelError, "ERROR", "", format, args...)
}
