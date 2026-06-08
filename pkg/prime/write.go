package prime

import (
	"fmt"
	"os"
	"strings"
)

// BlockStart and BlockEnd delimit the prime context written into a target file
// (e.g. CLAUDE.md or AGENTS.md). They let `md prime --write` find and replace a
// previously written block in place instead of duplicating it. They are HTML
// comments, so they stay invisible in rendered markdown.
const (
	BlockStart = "<!-- md-prime:start -->"
	BlockEnd   = "<!-- md-prime:end -->"
)

// block wraps content in the prime markers (no surrounding blank lines).
func block(content string) string {
	return BlockStart + "\n" + strings.TrimSpace(content) + "\n" + BlockEnd
}

// WriteFile writes the prime context into the file at path, wrapped in
// BlockStart/BlockEnd markers:
//
//   - if a marker-delimited block already exists, it is replaced in place,
//     preserving any text before and after it;
//   - else the block is appended to the end of the file;
//   - else (file missing or empty) the file is created containing just the block.
//
// It returns a verb describing the action taken: "created", "updated",
// "appended", or "unchanged".
func WriteFile(path, content string) (string, error) {
	blk := block(content)
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		if err := os.WriteFile(path, []byte(blk+"\n"), 0o644); err != nil {
			return "", err
		}
		return "created", nil
	}
	if err != nil {
		return "", fmt.Errorf("reading %s: %w", path, err)
	}
	text := string(data)
	// Replace an existing block in place (end marker must follow the start one).
	if s := strings.Index(text, BlockStart); s != -1 {
		if rel := strings.Index(text[s:], BlockEnd); rel != -1 {
			end := s + rel + len(BlockEnd)
			updated := text[:s] + blk + text[end:]
			if updated == text {
				return "unchanged", nil
			}
			if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
				return "", err
			}
			return "updated", nil
		}
	}
	// No block yet: append after a blank-line separator. A blank/whitespace-only
	// file gets just the block (treated as a fresh create).
	fresh := strings.TrimSpace(text) == ""
	var b strings.Builder
	if !fresh {
		b.WriteString(text)
		if !strings.HasSuffix(text, "\n") {
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}
	b.WriteString(blk)
	b.WriteByte('\n')
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		return "", err
	}
	if fresh {
		return "created", nil
	}
	return "appended", nil
}
