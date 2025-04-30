package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
	"unicode/utf8"
)

func debug(data json.RawMessage) {
	if env.Debug {
		if err := os.MkdirAll(env.LogsDir, 0755); err != nil {
			panic(fmt.Sprintf("error creating debug folder: %v", err))
		}
		debugLogFile := "debug-" + env.LogsSuffix + ".log"
		f, err := os.OpenFile(env.LogsDir+"/"+debugLogFile, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			panic(fmt.Sprintf("error opening log file: %v", err))
		}
		defer f.Close()
		type LogLine struct {
			Ts   string          `json:"ts"`
			Data json.RawMessage `json:"data"`
		}
		line, err := json.Marshal(LogLine{
			Ts:   time.Now().Format(time.RFC3339),
			Data: data,
		})
		if err != nil {
			panic(fmt.Sprintf("error marshalling log line: %v", err))
		}
		line = append(line, []byte("\n")...)
		if _, err := f.Write(line); err != nil {
			panic(fmt.Sprintf("error writing to log file: %v", err))
		}
	}
}
func debugString(msg string, args ...any) {
	d, err := json.Marshal(map[string]any{"msg": fmt.Sprintf(msg, args...)})
	if err != nil {
		panic(fmt.Sprintf("error marshalling debug message: %v", err))
	}
	debug(d)
}
func debugAny(data any) {
	d, err := json.Marshal(data)
	if err != nil {
		panic(fmt.Sprintf("error marshalling debug data: %v", err))
	}
	debug(d)
}

func wrap(s string, prefix string, width int) string {
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		wrapped := wrapLine(line, width-utf8.RuneCountInString(prefix))
		for j, wline := range wrapped {
			wrapped[j] = prefix + wline
		}
		lines[i] = strings.Join(wrapped, "\n")
	}
	return strings.Join(lines, "\n")
}
func wrapLine(s string, width int) []string {
	if width <= 0 {
		return []string{s}
	}
	var b strings.Builder
	curlen := 0
	words := strings.Split(s, " ")
	for i, word := range words {
		wlen := utf8.RuneCountInString(word)
		if i == 0 {
			b.WriteString(word)
			curlen += wlen
		} else if curlen+1+wlen <= width {
			b.WriteByte(' ')
			b.WriteString(word)
			curlen += 1 + wlen
		} else {
			b.WriteString("\n" + word)
			curlen = wlen
		}
	}
	return strings.Split(b.String(), "\n")
}
