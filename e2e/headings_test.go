package e2e

import (
	"testing"

	"github.com/jpillora/meads/pkg/meads"
)

func TestRaiseHeadings(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		minLevel int
		want     string
	}{
		{"empty", "", 3, ""},
		{"no headings", "just text\nmore text", 3, "just text\nmore text"},
		{"already at min", "### Title\n#### Sub", 3, "### Title\n#### Sub"},
		{"raise H1 to H3", "# Title\n## Sub\ntext", 3, "### Title\n#### Sub\ntext"},
		{"raise H2 to H3", "## Title\n### Sub", 3, "### Title\n#### Sub"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := meads.RaiseHeadings(tt.input, tt.minLevel)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestLowerHeadings(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"empty", "", ""},
		{"no headings", "just text", "just text"},
		{"first line heading becomes H1", "### Title\n#### Sub\ntext", "# Title\n## Sub\ntext"},
		{"first line not heading becomes H2", "intro\n### Title\n#### Sub", "intro\n## Title\n### Sub"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := meads.LowerHeadings(tt.input)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHeadings_RoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"heading first line", "# Title\n## Sub\ntext\n### Deep", "# Title\n## Sub\ntext\n### Deep"},
		{"no headings", "just plain text\nno headings here", "just plain text\nno headings here"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raised := meads.RaiseHeadings(tt.input, 3)
			lowered := meads.LowerHeadings(raised)
			if lowered != tt.want {
				t.Errorf("round-trip: got %q, want %q", lowered, tt.want)
			}
		})
	}
}
