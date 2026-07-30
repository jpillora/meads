package meads

import "testing"

func TestParseIntSlice(t *testing.T) {
	tests := []struct {
		input string
		want  []int
	}{
		{"1,2,3", []int{1, 2, 3}},
		{" 1 , 2 ", []int{1, 2}},
		{"", nil},
		{"abc", nil},
		{"1,abc,3", []int{1, 3}},
	}
	for _, tt := range tests {
		got := parseIntSlice(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("parseIntSlice(%q) = %v, want %v", tt.input, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("parseIntSlice(%q)[%d] = %d, want %d", tt.input, i, got[i], tt.want[i])
			}
		}
	}
}

func TestFormatIntSlice(t *testing.T) {
	tests := []struct {
		input []int
		want  string
	}{
		{[]int{1, 2, 3}, "1,2,3"},
		{[]int{}, ""},
		{nil, ""},
	}
	for _, tt := range tests {
		got := formatIntSlice(tt.input)
		if got != tt.want {
			t.Errorf("formatIntSlice(%v) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestSplitCSV(t *testing.T) {
	tests := []struct {
		input string
		want  []string
	}{
		{"a,b,c", []string{"a", "b", "c"}},
		{" a , b ", []string{"a", "b"}},
		{"", nil},
		{",,,", nil},
		{"a,,b", []string{"a", "b"}},
		{"a,b,a", []string{"a", "b"}}, // duplicates collapse
	}
	for _, tt := range tests {
		got := splitCSV(tt.input)
		if len(got) != len(tt.want) {
			t.Errorf("splitCSV(%q) = %v, want %v", tt.input, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("splitCSV(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
			}
		}
	}
}
