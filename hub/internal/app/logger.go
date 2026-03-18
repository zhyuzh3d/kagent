package app

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
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
 
type LogEntry struct {
	Timestamp string `json:"ts"`
	Level     string `json:"level"`
	Tag       string `json:"tag"`
	Name      string `json:"name"`
	Message   string `json:"msg"`
}
 
var logSubscribers []chan LogEntry
var logMu sync.Mutex
 
// SubscribeLogs registers a channel to receive log entries.
// Caller must call the returned unsubscribe function to clean up.
func SubscribeLogs() (chan LogEntry, func()) {
	logMu.Lock()
	defer logMu.Unlock()
	ch := make(chan LogEntry, 100)
	logSubscribers = append(logSubscribers, ch)
	return ch, func() {
		logMu.Lock()
		defer logMu.Unlock()
		for i, s := range logSubscribers {
			if s == ch {
				logSubscribers = append(logSubscribers[:i], logSubscribers[i+1:]...)
				close(ch)
				break
			}
		}
	}
}
 
func notifySubscribers(entry LogEntry) {
	logMu.Lock()
	defer logMu.Unlock()
	for _, ch := range logSubscribers {
		select {
		case ch <- entry:
		default:
			// Drop if subscriber is slow
		}
	}
}

// InitLogger initializes the global structured logger.
func InitLogger(level LogLevel, tag string) {
	currentLevel = level
	if strings.TrimSpace(tag) != "" {
		currentTag = strings.TrimSpace(tag)
	}
}

// formatLog constructs the string:
// [YYYY-MM-DD HH:MM:SS] [LEVEL] [TAG] Message
func formatLog(level LogLevel, levelStr string, tag string, format string, args ...any) {
	if level < currentLevel {
		return
	}

	useTag := currentTag
	if tag != "" {
		useTag = tag
	}

	msg := format
	if len(args) > 0 {
		msg = fmt.Sprintf(format, args...)
	}

	ts := time.Now().Format("2006-01-02 15:04:05")
	fmt.Fprintf(os.Stdout, "[%s] [%s] [%s] %s\n", ts, levelStr, useTag, msg)
}

// formatLogIdentity constructs an identity-aware log line:
// [YYYY-MM-DD HH:MM:SS] [LEVEL] [TAG] [NAME] Message
func formatLogIdentity(level LogLevel, levelStr string, tag string, identityName string, format string, args ...any) {
	if level < currentLevel {
		return
	}

	useTag := currentTag
	if tag != "" {
		useTag = tag
	}

	msg := format
	if len(args) > 0 {
		msg = fmt.Sprintf(format, args...)
	}

	ts := time.Now().Format("2006-01-02 15:04:05")
	name := strings.TrimSpace(identityName)
 
	entry := LogEntry{
		Timestamp: ts,
		Level:     levelStr,
		Tag:       useTag,
		Name:      name,
		Message:   msg,
	}
	notifySubscribers(entry)
 
	if name == "" {
		// Fallback to non-identity format
		fmt.Fprintf(os.Stdout, "[%s] [%s] [%s] %s\n", ts, levelStr, useTag, msg)
		return
	}
	fmt.Fprintf(os.Stdout, "[%s] [%s] [%s] [%s] %s\n", ts, levelStr, useTag, name, msg)
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

// InfofCtx logs at LevelInfo with identity from context.
func InfofCtx(ctx context.Context, format string, args ...any) {
	id := IdentityFromContext(ctx)
	formatLogIdentity(LevelInfo, "INFO", "", id.Name, format, args...)
}

// InfofCtxTag logs at LevelInfo with a custom tag and identity from context.
func InfofCtxTag(ctx context.Context, tag string, format string, args ...any) {
	id := IdentityFromContext(ctx)
	formatLogIdentity(LevelInfo, "INFO", tag, id.Name, format, args...)
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

// WarnfCtx logs at LevelWarn with identity from context.
func WarnfCtx(ctx context.Context, format string, args ...any) {
	id := IdentityFromContext(ctx)
	formatLogIdentity(LevelWarn, "WARN", "", id.Name, format, args...)
}

// Errorf logs at LevelError
func Errorf(format string, args ...any) {
	formatLog(LevelError, "ERROR", "", format, args...)
}

// ErrorfCtx logs at LevelError with identity from context.
func ErrorfCtx(ctx context.Context, format string, args ...any) {
	id := IdentityFromContext(ctx)
	formatLogIdentity(LevelError, "ERROR", "", id.Name, format, args...)
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
