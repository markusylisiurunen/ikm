package logger

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"sync"
	"time"
)

type Logger interface {
	SetEnabled(enabled bool)
	SetLevel(level string)
	Debugf(msg string, args ...any)
	Debugj(msg string, data json.RawMessage)
	Errorf(msg string, args ...any)
	Errorj(msg string, data json.RawMessage)
}

//--------------------------------------------------------------------------------------------------

var _ Logger = (*noOpLogger)(nil)

type noOpLogger struct{}

func NoOp() Logger {
	return &noOpLogger{}
}

func (n *noOpLogger) SetEnabled(_ bool)                  {}
func (n *noOpLogger) SetLevel(_ string)                  {}
func (n *noOpLogger) Debugf(_ string, _ ...any)          {}
func (n *noOpLogger) Debugj(_ string, _ json.RawMessage) {}
func (n *noOpLogger) Errorf(_ string, _ ...any)          {}
func (n *noOpLogger) Errorj(_ string, _ json.RawMessage) {}

//--------------------------------------------------------------------------------------------------

var _ Logger = (*logger)(nil)

type logger struct {
	mux     sync.RWMutex
	enabled bool
	level   string
	file    *os.File
}

func New(file *os.File) Logger {
	return &logger{
		enabled: true,
		level:   "error",
		file:    file,
	}
}

func (l *logger) SetEnabled(enabled bool) {
	l.mux.Lock()
	defer l.mux.Unlock()
	l.enabled = enabled
}

func (l *logger) SetLevel(level string) {
	l.mux.Lock()
	defer l.mux.Unlock()
	l.level = level
}

func (l *logger) Debugf(msg string, args ...any) {
	l.logf("debug", msg, args...)
}

func (l *logger) Debugj(msg string, data json.RawMessage) {
	l.logj("debug", msg, data)
}

func (l *logger) Errorf(msg string, args ...any) {
	l.logf("error", msg, args...)
}

func (l *logger) Errorj(msg string, data json.RawMessage) {
	l.logj("error", msg, data)
}

type logLineData struct {
	Ts      string          `json:"ts"`
	Level   string          `json:"level"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitzero"`
}

func (l *logger) logf(level string, msg string, args ...any) {
	l.mux.RLock()
	_enabled, _level := l.enabled, l.level
	l.mux.RUnlock()
	if !_enabled || l.file == nil {
		return
	}
	levels := []string{"debug", "error"}
	logLevelIdx, loggerLevelIdx := slices.Index(levels, level), slices.Index(levels, _level)
	if logLevelIdx < 0 || loggerLevelIdx < 0 {
		panic(fmt.Sprintf("invalid log level: %s", level))
	}
	if logLevelIdx < loggerLevelIdx {
		return
	}
	logLineDataBytes, err := json.Marshal(logLineData{
		Ts:      time.Now().Format(time.RFC3339),
		Level:   level,
		Message: fmt.Sprintf(msg, args...),
	})
	if err != nil {
		panic(fmt.Sprintf("error marshalling log line: %v", err))
	}
	if _, err := l.file.Write(append(logLineDataBytes, '\n')); err != nil {
		panic(fmt.Sprintf("error writing log line: %v", err))
	}
	if err := l.file.Sync(); err != nil {
		panic(fmt.Sprintf("error syncing log file: %v", err))
	}
}

func (l *logger) logj(level string, msg string, data json.RawMessage) {
	l.mux.RLock()
	_enabled, _level := l.enabled, l.level
	l.mux.RUnlock()
	if !_enabled || l.file == nil {
		return
	}
	levels := []string{"debug", "error"}
	logLevelIdx, loggerLevelIdx := slices.Index(levels, level), slices.Index(levels, _level)
	if logLevelIdx < 0 || loggerLevelIdx < 0 {
		panic(fmt.Sprintf("invalid log level: %s", level))
	}
	if logLevelIdx < loggerLevelIdx {
		return
	}
	logLineDataBytes, err := json.Marshal(logLineData{
		Ts:      time.Now().Format(time.RFC3339),
		Level:   level,
		Message: msg,
		Data:    data,
	})
	if err != nil {
		panic(fmt.Sprintf("error marshalling log line: %v", err))
	}
	if _, err := l.file.Write(append(logLineDataBytes, '\n')); err != nil {
		panic(fmt.Sprintf("error writing log line: %v", err))
	}
	if err := l.file.Sync(); err != nil {
		panic(fmt.Sprintf("error syncing log file: %v", err))
	}
}
