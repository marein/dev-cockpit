package clirun

import (
	"errors"
	"fmt"
	"strings"
)

// SplitCommand splits a configured command line into argv the way a shell
// splits words, and no further: spaces separate, quotes group, a backslash
// takes the next character as it stands. Nothing is expanded, no variable, no
// pattern against the disk, no command inside another, because the line is
// never handed to a shell in the first place. What comes out of here reaches
// the program as arguments and can never become a command of its own.
func SplitCommand(line string) ([]string, error) {
	var (
		argv    []string
		word    strings.Builder
		started bool
		quote   rune
	)
	flush := func() {
		if started {
			argv = append(argv, word.String())
			word.Reset()
			started = false
		}
	}
	runes := []rune(line)
	for i := 0; i < len(runes); i++ {
		c := runes[i]
		switch {
		case quote == '\'':
			if c == '\'' {
				quote = 0
				continue
			}
			word.WriteRune(c)
		case quote == '"':
			if c == '"' {
				quote = 0
				continue
			}
			if c == '\\' && i+1 < len(runes) && (runes[i+1] == '"' || runes[i+1] == '\\') {
				i++
				word.WriteRune(runes[i])
				continue
			}
			word.WriteRune(c)
		case c == '\'' || c == '"':
			quote = c
			started = true
		case c == '\\':
			if i+1 >= len(runes) {
				return nil, errors.New("the command ends in a backslash")
			}
			i++
			word.WriteRune(runes[i])
			started = true
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			flush()
		default:
			word.WriteRune(c)
			started = true
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("the command has an unclosed %c quote", quote)
	}
	flush()
	return argv, nil
}
