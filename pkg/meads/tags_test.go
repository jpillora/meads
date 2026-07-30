package meads

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestParseTagsRoundTrip(t *testing.T) {
	tests := []struct {
		input string
		want  Tags
		csv   string
	}{
		{"api,backend", Tags{"api", "backend"}, "api,backend"},
		{" api , backend ", Tags{"api", "backend"}, "api,backend"},
		{"api,api", Tags{"api"}, "api"},
		{"api,,backend,", Tags{"api", "backend"}, "api,backend"},
		{"", nil, ""},
		{",", nil, ""},
		// Lenient by design: decoding never rejects, so a value that
		// predates the tag rule still loads.
		{"Not A Tag", Tags{"Not A Tag"}, "Not A Tag"},
	}
	for _, tt := range tests {
		got := ParseTags(tt.input)
		if got.String() != tt.csv {
			t.Errorf("ParseTags(%q).String() = %q, want %q", tt.input, got.String(), tt.csv)
		}
		if len(got) != len(tt.want) {
			t.Errorf("ParseTags(%q) = %v, want %v", tt.input, got, tt.want)
			continue
		}
		for i := range got {
			if got[i] != tt.want[i] {
				t.Errorf("ParseTags(%q)[%d] = %q, want %q", tt.input, i, got[i], tt.want[i])
			}
		}
	}
}

func TestNormalizeTags(t *testing.T) {
	valid := []struct {
		input string
		want  string
	}{
		{"api,backend", "api,backend"},
		{"API, Backend", "api,backend"},
		{"web-ui,web-ui", "web-ui"},
		{"p0,x2,9", "p0,x2,9"},
		{"", ""},
	}
	for _, tt := range valid {
		got, err := NormalizeTags(tt.input)
		if err != nil {
			t.Errorf("NormalizeTags(%q) error: %v", tt.input, err)
			continue
		}
		if got.String() != tt.want {
			t.Errorf("NormalizeTags(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
	invalid := []string{"web ui", "web_ui", "web/ui", "wébui", "api!", "Ünicode"}
	for _, in := range invalid {
		got, err := NormalizeTags(in)
		if err == nil {
			t.Errorf("NormalizeTags(%q) = %q, want error", in, got)
			continue
		}
		if !strings.Contains(err.Error(), "lowercase letters, numbers and dashes") {
			t.Errorf("NormalizeTags(%q) error = %v, want the tag rule spelled out", in, err)
		}
	}
}

func TestTagsSetOps(t *testing.T) {
	tags := Tags{"api", "backend"}
	if !tags.Has("api") || tags.Has("frontend") {
		t.Errorf("Has: %v", tags)
	}
	if !tags.HasAll(Tags{"backend", "api"}) {
		t.Errorf("HasAll(subset) = false, want true")
	}
	if tags.HasAll(Tags{"api", "frontend"}) {
		t.Errorf("HasAll(superset) = true, want false")
	}
	// An empty filter matches everything, so callers can pass one through
	// unconditionally.
	if !tags.HasAll(nil) || !(Tags{}).HasAll(nil) {
		t.Errorf("HasAll(nil) = false, want true")
	}
	if got := tags.Add(Tags{"api", "docs"}).String(); got != "api,backend,docs" {
		t.Errorf("Add = %q, want api,backend,docs", got)
	}
	if got := tags.Remove(Tags{"api", "missing"}).String(); got != "backend" {
		t.Errorf("Remove = %q, want backend", got)
	}
	// Neither op mutates the receiver.
	if got := tags.String(); got != "api,backend" {
		t.Errorf("receiver mutated: %q", got)
	}
}

func TestSanitizeTags(t *testing.T) {
	got := SanitizeTags([]string{"Area/API", "  spaced  out ", "ok-tag", "!!!", "", "Area/API", "--trim--"})
	if want := "area-api,spaced-out,ok-tag,trim"; got.String() != want {
		t.Errorf("SanitizeTags = %q, want %q", got, want)
	}
	if err := got.Validate(); err != nil {
		t.Errorf("SanitizeTags produced an invalid tag: %v", err)
	}
}

func TestTagsUnmarshalJSON(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{`["api","backend"]`, "api,backend"},       // canonical array form
		{`"api,backend"`, "api,backend"},           // CSV form
		{`" api , backend , api "`, "api,backend"}, // CSV form, cleaned
		{`["api","backend","api"]`, "api,backend"}, // array form, cleaned
		{`null`, ""}, // absent
		{`[]`, ""},   // explicitly empty
	}
	for _, tt := range tests {
		var got Tags
		if err := json.Unmarshal([]byte(tt.input), &got); err != nil {
			t.Errorf("Unmarshal(%s) error: %v", tt.input, err)
			continue
		}
		if got.String() != tt.want {
			t.Errorf("Unmarshal(%s) = %q, want %q", tt.input, got, tt.want)
		}
	}
	var bad Tags
	if err := json.Unmarshal([]byte(`42`), &bad); err == nil {
		t.Errorf("Unmarshal(42) = %v, want error", bad)
	}
}

// TestTaskTagsJSON pins the wire form the git-mode blob and every JSON
// consumer see: an array, omitted entirely when empty.
func TestTaskTagsJSON(t *testing.T) {
	raw, err := json.Marshal(Task{ID: 1, Title: "t", Tags: Tags{"api", "backend"}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(raw), `"tags":["api","backend"]`) {
		t.Errorf("marshal = %s, want an array under \"tags\"", raw)
	}
	if raw, _ = json.Marshal(Task{ID: 1, Title: "t"}); strings.Contains(string(raw), "tags") {
		t.Errorf("marshal (no tags) = %s, want no \"tags\" key", raw)
	}
	// Round-trip back through a plain unmarshal, the path GitStore reads
	// task.json with.
	var back Task
	raw, _ = json.Marshal(Task{ID: 1, Title: "t", Tags: Tags{"api", "backend"}})
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if back.Tags.String() != "api,backend" {
		t.Errorf("round-tripped tags = %q, want api,backend", back.Tags)
	}
}

func TestSetTagsSyncsMeta(t *testing.T) {
	var task Task
	task.SetTags(Tags{"api", "backend"})
	if task.Meta["tags"] != "api,backend" {
		t.Errorf("Meta[tags] = %q, want api,backend", task.Meta["tags"])
	}
	// Clearing drops the key rather than leaving an empty "* tags:" line.
	task.SetTags(nil)
	if v, ok := task.Meta["tags"]; ok {
		t.Errorf("Meta[tags] = %q after clear, want absent", v)
	}
	if len(task.Tags) != 0 {
		t.Errorf("Tags = %v after clear, want empty", task.Tags)
	}
}
