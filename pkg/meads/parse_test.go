package meads

import "testing"

func TestSplitTitleDescription(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantTitle string
		wantDesc  string
	}{
		{"period space splits", "Fix bug. Details here", "Fix bug", "Details here"},
		{"period space trims extra spacing", "Fix bug.   Details", "Fix bug", "Details"},
		{"newline splits", "Fix bug\nDetails here", "Fix bug", "Details here"},
		// The "less harsh" case: a period followed by non-space whitespace
		// (here a tab) must NOT split — the old \.\s regex did.
		{"period tab does not split", "Fix bug.\tDetails", "Fix bug.\tDetails", ""},
		// Period immediately before a newline splits at the newline, so the
		// title retains its trailing period.
		{"period before newline keeps period", "Fix bug.\nDetails", "Fix bug.", "Details"},
		{"leftmost newline wins over later period-space", "Fix bug\nDetails. More", "Fix bug", "Details. More"},
		{"no delimiter", "Just a title", "Just a title", ""},
		{"trailing period no space does not split", "Ship it.", "Ship it.", ""},
		{"url period does not split", "Investigate http://foo.com latency. See dashboard", "Investigate http://foo.com latency", "See dashboard"},
		{"decimal version does not split", "Ship v1.2.3 today\nrollout notes", "Ship v1.2.3 today", "rollout notes"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotTitle, gotDesc := SplitTitleDescription(tt.input)
			if gotTitle != tt.wantTitle {
				t.Errorf("title: got %q, want %q", gotTitle, tt.wantTitle)
			}
			if gotDesc != tt.wantDesc {
				t.Errorf("desc: got %q, want %q", gotDesc, tt.wantDesc)
			}
		})
	}
}
