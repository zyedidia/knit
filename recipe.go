package main

import (
	"bufio"
	"strings"
	"unicode/utf8"
)

// Try to unindent a recipe, so that it begins an column 0.
func stripIndentation(s string, mincol int) string {
	// trim leading whitespace
	reader := bufio.NewReader(strings.NewReader(s))
	output := ""
	for {
		line, err := reader.ReadString('\n')
		col := 0
		i := 0
		for i < len(line) && col < mincol {
			c, w := utf8.DecodeRuneInString(line[i:])
			if strings.ContainsRune(" \t\n", c) {
				col += 1
				i += w
			} else {
				break
			}
		}
		output += line[i:]

		if err != nil {
			break
		}
	}

	return output
}
