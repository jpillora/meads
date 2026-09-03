package meads

import (
	"strings"
)

// RaiseHeadings shifts all markdown headings in s so the minimum level is
// minLevel (e.g. 3 for H3). All headings are incremented by the same offset
// to preserve their relative hierarchy. Returns s unchanged if there are no
// headings or the minimum is already >= minLevel.
func RaiseHeadings(s string, minLevel int) string {
	if s == "" {
		return s
	}
	lines := strings.Split(s, "\n")
	currentMin := headingMinLevel(lines)
	if currentMin == 0 || currentMin >= minLevel {
		return s
	}
	offset := minLevel - currentMin
	return shiftHeadings(lines, offset)
}

// LowerHeadings shifts all markdown headings in s downward. If the first line
// is a heading, the minimum heading level becomes H1. Otherwise the minimum
// becomes H2. Returns s unchanged if there are no headings.
func LowerHeadings(s string) string {
	if s == "" {
		return s
	}
	lines := strings.Split(s, "\n")
	currentMin := headingMinLevel(lines)
	if currentMin == 0 {
		return s
	}
	target := 2
	if headingLevel(lines[0]) > 0 {
		target = 1
	}
	offset := target - currentMin
	if offset == 0 {
		return s
	}
	return shiftHeadings(lines, offset)
}

// headingLevel returns the heading level of a markdown line (1-6) or 0 if
// the line is not a heading.
func headingLevel(line string) int {
	level := 0
	for _, c := range line {
		if c == '#' {
			level++
		} else {
			break
		}
	}
	if level == 0 || level > 6 {
		return 0
	}
	// Must be followed by a space (standard markdown).
	if len(line) > level && line[level] == ' ' {
		return level
	}
	return 0
}

// headingMinLevel returns the smallest heading level found across lines,
// or 0 if no headings are present.
func headingMinLevel(lines []string) int {
	min := 0
	for _, line := range lines {
		if lvl := headingLevel(line); lvl > 0 {
			if min == 0 || lvl < min {
				min = lvl
			}
		}
	}
	return min
}

// shiftHeadings applies an offset to every heading line. Positive offset
// increases the level (e.g. H1→H3 with offset +2), negative decreases it.
// Heading levels are clamped to [1, 6].
func shiftHeadings(lines []string, offset int) string {
	result := make([]string, len(lines))
	for i, line := range lines {
		lvl := headingLevel(line)
		if lvl == 0 {
			result[i] = line
			continue
		}
		newLevel := lvl + offset
		if newLevel < 1 {
			newLevel = 1
		}
		if newLevel > 6 {
			newLevel = 6
		}
		// Replace the heading prefix, keeping the rest of the line.
		result[i] = strings.Repeat("#", newLevel) + line[lvl:]
	}
	return strings.Join(result, "\n")
}
