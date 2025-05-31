package tui

import (
	"strings"
	"unicode/utf8"
)

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

func wrapWithPrefix(s string, prefix string, width int) string {
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
