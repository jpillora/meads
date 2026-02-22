package meads

import (
	"testing"
)

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

func TestRaiseHeadings(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		minLevel int
		want     string
	}{
		{
			name:     "empty",
			input:    "",
			minLevel: 3,
			want:     "",
		},
		{
			name:     "no headings",
			input:    "just text\nmore text",
			minLevel: 3,
			want:     "just text\nmore text",
		},
		{
			name:     "already at min",
			input:    "### Title\n#### Sub",
			minLevel: 3,
			want:     "### Title\n#### Sub",
		},
		{
			name:     "already above min",
			input:    "#### Title\n##### Sub",
			minLevel: 3,
			want:     "#### Title\n##### Sub",
		},
		{
			name:     "raise H1 to H3",
			input:    "# Title\n## Sub\ntext",
			minLevel: 3,
			want:     "### Title\n#### Sub\ntext",
		},
		{
			name:     "raise H2 to H3",
			input:    "## Title\n### Sub",
			minLevel: 3,
			want:     "### Title\n#### Sub",
		},
		{
			name:     "clamp at H6",
			input:    "# Title\n##### Deep",
			minLevel: 3,
			want:     "### Title\n###### Deep",
		},
		{
			name:     "text before heading",
			input:    "intro\n# Title\n## Sub",
			minLevel: 3,
			want:     "intro\n### Title\n#### Sub",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := RaiseHeadings(tt.input, tt.minLevel)
			if got != tt.want {
				t.Errorf("RaiseHeadings(%q, %d):\ngot:  %q\nwant: %q", tt.input, tt.minLevel, got, tt.want)
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
		{
			name:  "empty",
			input: "",
			want:  "",
		},
		{
			name:  "no headings",
			input: "just text",
			want:  "just text",
		},
		{
			name:  "first line heading becomes H1",
			input: "### Title\n#### Sub\ntext",
			want:  "# Title\n## Sub\ntext",
		},
		{
			name:  "first line not heading becomes H2",
			input: "intro\n### Title\n#### Sub",
			want:  "intro\n## Title\n### Sub",
		},
		{
			name:  "already at target H1",
			input: "# Title\n## Sub",
			want:  "# Title\n## Sub",
		},
		{
			name:  "already at target H2 non-heading first",
			input: "text\n## Title\n### Sub",
			want:  "text\n## Title\n### Sub",
		},
		{
			name:  "single heading first line",
			input: "#### Only",
			want:  "# Only",
		},
		{
			name:  "single heading not first line",
			input: "text\n#### Only",
			want:  "text\n## Only",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := LowerHeadings(tt.input)
			if got != tt.want {
				t.Errorf("LowerHeadings(%q):\ngot:  %q\nwant: %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestRoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "heading first line",
			input: "# Title\n## Sub\ntext\n### Deep",
			want:  "# Title\n## Sub\ntext\n### Deep",
		},
		{
			// When first line is text, headings lower to H2 (not H1),
			// so round-trip shifts H1 → H2.
			name:  "text first line",
			input: "intro text\n# Title\n## Sub",
			want:  "intro text\n## Title\n### Sub",
		},
		{
			name:  "no headings",
			input: "just plain text\nno headings here",
			want:  "just plain text\nno headings here",
		},
		{
			// Stable round-trip: text first + H2 min → raise to H3 → lower to H2.
			name:  "text first H2 stable",
			input: "intro\n## Title\n### Sub",
			want:  "intro\n## Title\n### Sub",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			raised := RaiseHeadings(tt.input, 3)
			lowered := LowerHeadings(raised)
			if lowered != tt.want {
				t.Errorf("round-trip:\ninput:   %q\nraised:  %q\nlowered: %q\nwant:    %q", tt.input, raised, lowered, tt.want)
			}
		})
	}
}
