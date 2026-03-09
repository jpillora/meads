package meads

import "testing"

// Whitebox tests for unexported heading helpers.

func TestHeadingLevel(t *testing.T) {
	tests := []struct {
		line string
		want int
	}{
		{"# Title", 1},
		{"## Sub", 2},
		{"### Deep", 3},
		{"###### Max", 6},
		{"no heading", 0},
		{"#nospace", 0},
		{"", 0},
		{"####### too many", 0},
	}
	for _, tt := range tests {
		got := headingLevel(tt.line)
		if got != tt.want {
			t.Errorf("headingLevel(%q) = %d, want %d", tt.line, got, tt.want)
		}
	}
}

func TestShiftHeadings_ClampToOne(t *testing.T) {
	lines := []string{"## Heading"}
	got := shiftHeadings(lines, -5)
	want := "# Heading"
	if got != want {
		t.Errorf("shiftHeadings with clamp = %q, want %q", got, want)
	}
}
