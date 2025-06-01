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
	var currentLine strings.Builder
	curlen := 0
	words := strings.Split(s, " ")

	for i, word := range words {
		wlen := utf8.RuneCountInString(word)

		// Handle the first word or if the word fits on the current line
		if i == 0 {
			if wlen <= width {
				currentLine.WriteString(word)
				curlen += wlen
			} else {
				// Word is too long, split it at character boundaries
				lines := splitLongWord(word, width)
				result = append(result, lines[:len(lines)-1]...)
				currentLine.WriteString(lines[len(lines)-1])
				curlen = utf8.RuneCountInString(lines[len(lines)-1])
			}
		} else if curlen+1+wlen <= width {
			// Word fits on current line with a space
			currentLine.WriteByte(' ')
			currentLine.WriteString(word)
			curlen += 1 + wlen
		} else {
			// Word doesn't fit, start a new line
			result = append(result, currentLine.String())
			currentLine.Reset()

			if wlen <= width {
				currentLine.WriteString(word)
				curlen = wlen
			} else {
				// Word is too long, split it at character boundaries
				lines := splitLongWord(word, width)
				result = append(result, lines[:len(lines)-1]...)
				currentLine.WriteString(lines[len(lines)-1])
				curlen = utf8.RuneCountInString(lines[len(lines)-1])
			}
		}
	}

	if currentLine.Len() > 0 {
		result = append(result, currentLine.String())
	}

	return result
}

// splitLongWord splits a word that's longer than width at character boundaries
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
