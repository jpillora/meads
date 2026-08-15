package e2e

import (
	"strings"
	"testing"

	"github.com/jpillora/meads/pkg/meads"
)

// Tags are one CSV metadata value in every backend. These tests pin that
// they survive a full write/read round trip through each storage format the
// same way - markdown's "* tags:" line, CSV's "tags" column, and git mode's
// task.json array (pkg/meads/tags_test.go covers the JSON form directly,
// and cmd/md/tags_test.go drives the same through the real commands).

func TestTags_MarkdownRoundTrip(t *testing.T) {
	store := newMDStore(t)
	id, err := store.Add(taskWithTags("Tagged task", "api", "web-ui"))
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	content := readStore(t, store)
	if !strings.Contains(content, "* tags: api,web-ui\n") {
		t.Errorf("TASKS.md missing the CSV tags line:\n%s", content)
	}
	if got := getTask(t, store, id).Tags.String(); got != "api,web-ui" {
		t.Errorf("re-read tags = %q, want api,web-ui", got)
	}
	// Clearing removes the line rather than leaving "* tags:" behind.
	if err := store.Update(id, func(task *meads.Task) { task.SetTags(nil) }); err != nil {
		t.Fatalf("update: %v", err)
	}
	if content := readStore(t, store); strings.Contains(content, "tags") {
		t.Errorf("TASKS.md still mentions tags after clearing:\n%s", content)
	}
	if got := getTask(t, store, id).Tags; len(got) != 0 {
		t.Errorf("re-read tags = %v, want none", got)
	}
}

func TestTags_CSVRoundTrip(t *testing.T) {
	store := newCSVStore(t)
	id, err := store.Add(taskWithTags("Tagged task", "api", "web-ui"))
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if content := readStore(t, store); !strings.Contains(content, `"api,web-ui"`) {
		t.Errorf("TASKS.csv missing the CSV tags column:\n%s", content)
	}
	if got := getTask(t, store, id).Tags.String(); got != "api,web-ui" {
		t.Errorf("re-read tags = %q, want api,web-ui", got)
	}
}

// TestTags_MarkdownFieldIsAuthoritative covers the case the formatter has
// to synthesize: a task whose Tags field was set directly, with no Meta
// entry to format from - exactly the shape every git-sourced task has.
// Tags was the first key to get this treatment; TASKS #92 extended it to
// all ten field-backed keys (see markdown.go's taskMetaForFormat).
func TestTags_MarkdownFieldIsAuthoritative(t *testing.T) {
	out := meads.FormatTask(meads.Task{ID: 7, Title: "Direct", Tags: meads.Tags{"api"}})
	if !strings.Contains(out, "* tags: api\n") {
		t.Errorf("FormatTask dropped a directly-set Tags field:\n%s", out)
	}
	// And the reverse: a stale Meta entry never outlives a cleared field.
	stale := meads.Task{ID: 7, Title: "Stale", Meta: map[string]string{"tags": "api"}}
	if out := meads.FormatTask(stale); strings.Contains(out, "tags") {
		t.Errorf("FormatTask kept a stale meta tags entry:\n%s", out)
	}
}

// TestTags_MarkdownParseIsLenient proves a hand-written value still loads:
// decoding never enforces the tag rule (that happens at the input
// boundaries), it only cleans up the CSV.
func TestTags_MarkdownParseIsLenient(t *testing.T) {
	f := meads.ParseFile("## 1 Legacy\n\n* tags: API, api ,, Not A Tag\n")
	if len(f.Tasks) != 1 {
		t.Fatalf("expected 1 task, got %d", len(f.Tasks))
	}
	if got := f.Tasks[0].Tags.String(); got != "API,api,Not A Tag" {
		t.Errorf("tags = %q, want API,api,Not A Tag", got)
	}
}

func taskWithTags(title string, tags ...string) meads.Task {
	task := meads.Task{Title: title}
	task.SetStatus("open")
	task.SetTags(meads.Tags(tags))
	return task
}

func readStore(t *testing.T, store *meads.Store) string {
	t.Helper()
	f, err := store.FS().Open(store.Path())
	if err != nil {
		t.Fatalf("opening %s: %v", store.Path(), err)
	}
	defer f.Close()
	var sb strings.Builder
	buf := make([]byte, 4096)
	for {
		n, err := f.Read(buf)
		sb.Write(buf[:n])
		if err != nil || n == 0 {
			break
		}
	}
	return sb.String()
}

func getTask(t *testing.T, store *meads.Store, id int) meads.Task {
	t.Helper()
	tasks, err := store.Get([]int{id})
	if err != nil {
		t.Fatalf("get %d: %v", id, err)
	}
	return tasks[0]
}
