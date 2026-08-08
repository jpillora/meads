package main

import (
	"fmt"
	"io"
	"os"
	"strings"
)

// readDescriptionFile reads a description from path. Like many Unix tools,
// the special path "-" means stdin; this makes a quoted shell HEREDOC a safe
// way to pass rich Markdown without the shell interpreting backticks, dollar
// signs, or backslashes.
func readDescriptionFile(path string, stdin io.Reader) (string, error) {
	var (
		data []byte
		err  error
	)
	if path == "-" {
		if stdin == nil {
			stdin = os.Stdin
		}
		data, err = io.ReadAll(stdin)
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return "", fmt.Errorf("reading description file: %w", err)
	}

	// A text file and a HEREDOC conventionally end in a newline, but that
	// terminator is not part of the description. Removing terminal line
	// endings also keeps the serialized tasks file in its canonical form.
	return strings.TrimRight(string(data), "\r\n"), nil
}

func (g *globals) stdinReader() io.Reader {
	if g.Stdin != nil {
		return g.Stdin
	}
	return os.Stdin
}
