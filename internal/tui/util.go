package tui

import (
	"strings"
	"unicode/utf8"
)

func wrapLine(s string, width int) []string {
	if width <= 0 {
		return []string{s}
	}
	var result []string
	var curline strings.Builder
	var curlen int = 0
	for i, word := range strings.Split(s, " ") {
		wlen := utf8.RuneCountInString(word)
		if i == 0 {
			if wlen <= width {
				curline.WriteString(word)
				curlen += wlen
			} else {
				lines := splitLongWord(word, width)
				result = append(result, lines[:len(lines)-1]...)
				curline.WriteString(lines[len(lines)-1])
				curlen = utf8.RuneCountInString(lines[len(lines)-1])
			}
		} else if curlen+1+wlen <= width {
			curline.WriteByte(' ')
			curline.WriteString(word)
			curlen += 1 + wlen
		} else {
			result = append(result, curline.String())
			curline.Reset()
			if wlen <= width {
				curline.WriteString(word)
				curlen = wlen
			} else {
				lines := splitLongWord(word, width)
				result = append(result, lines[:len(lines)-1]...)
				curline.WriteString(lines[len(lines)-1])
				curlen = utf8.RuneCountInString(lines[len(lines)-1])
			}
		}
	}
	if curline.Len() > 0 {
		result = append(result, curline.String())
	}
	return result
}

func splitLongWord(word string, width int) []string {
	if width <= 0 {
		return []string{word}
	}
	var lines []string
	runes := []rune(word)
	for len(runes) > 0 {
		if len(runes) <= width {
			lines = append(lines, string(runes))
			break
		}
		lines = append(lines, string(runes[:width]))
		runes = runes[width:]
	}
	return lines
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
