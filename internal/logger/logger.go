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
	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Error(msg string, args ...any)
}

//--------------------------------------------------------------------------------------------------

var _ Logger = (*noOpLogger)(nil)

type noOpLogger struct{}

func NoOp() Logger {
	return &noOpLogger{}
}

func (n *noOpLogger) SetEnabled(_ bool)        {}
func (n *noOpLogger) SetLevel(_ string)        {}
func (n *noOpLogger) Debug(_ string, _ ...any) {}
func (n *noOpLogger) Info(_ string, _ ...any)  {}
func (n *noOpLogger) Error(_ string, _ ...any) {}

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

func (l *logger) Debug(msg string, args ...any) {
	l.log("debug", msg, args...)
}

func (l *logger) Info(msg string, args ...any) {
	l.log("info", msg, args...)
}

func (l *logger) Error(msg string, args ...any) {
	l.log("error", msg, args...)
}

type logLineData struct {
	Ts      string `json:"ts"`
	Level   string `json:"level"`
	Message string `json:"message"`
}

func (l *logger) log(level string, msg string, args ...any) {
	l.mux.RLock()
	_enabled, _level := l.enabled, l.level
	l.mux.RUnlock()
	if !_enabled || l.file == nil {
		return
	}
	levels := []string{"debug", "info", "error"}
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
