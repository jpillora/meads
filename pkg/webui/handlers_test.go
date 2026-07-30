package webui

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/jpillora/meads/pkg/meads"
)

// newTestServer wires up a Server and an httptest.Server using memfs-backed storage.
// The watcher/bind hubs remain in-process; tests that don't exercise the listener
// never call Run() directly.
func newTestServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	// git is nil: the web UI never calls the history methods
	// (GetWithHistory/GetHistory) that would need it - see meads.FileTasks'
	// doc comment.
	store := meads.NewFileTasks(meads.NewStore(memfs.New(), "TASKS.md"), nil)
	s, err := New(Config{Store: store, Token: "secret-test-token", Print: "none"})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(withMiddleware(s.routes(), s.Token()))
	t.Cleanup(func() {
		ts.Close()
		s.bind.closeAll()
		s.events.closeAll()
	})
	return ts, s.Token()
}

func do(t *testing.T, ts *httptest.Server, token, method, path string, body any) (*http.Response, []byte) {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, ts.URL+path, r)
	if err != nil {
		t.Fatal(err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return resp, data
}

func TestAuth_Unauthorized(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, _ := do(t, ts, "", http.MethodGet, "/api/tasks", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

func TestAuth_TokenQueryParam(t *testing.T) {
	ts, token := newTestServer(t)
	resp, err := http.Get(ts.URL + "/api/tasks?token=" + token)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestAuth_StaticAssetsExempt(t *testing.T) {
	ts, _ := newTestServer(t)
	resp, err := http.Get(ts.URL + "/app.js")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected static asset to be served without auth, got %d", resp.StatusCode)
	}
}

func TestListTasks_Empty(t *testing.T) {
	ts, token := newTestServer(t)
	resp, body := do(t, ts, token, http.MethodGet, "/api/tasks", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	var got []meads.Task
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v body=%s", err, body)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 tasks, got %d", len(got))
	}
}

func TestAddTask_AndFetch(t *testing.T) {
	ts, token := newTestServer(t)
	resp, body := do(t, ts, token, http.MethodPost, "/api/tasks",
		meads.AddTaskInput{Title: "Write tests", Priority: "P1", Type: "task", Description: "e2e coverage"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	var out meads.AddTaskOutput
	if err := json.Unmarshal(body, &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.ID != 1 {
		t.Errorf("expected id=1, got %d", out.ID)
	}

	// Fetch it back.
	resp, body = do(t, ts, token, http.MethodGet, "/api/tasks/1", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get status=%d body=%s", resp.StatusCode, body)
	}
	var got meads.Task
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Title != "Write tests" || got.Priority != "P1" || got.Type != "task" {
		t.Errorf("unexpected task: %+v", got)
	}
}

func TestAddTask_RequiresTitle(t *testing.T) {
	ts, token := newTestServer(t)
	resp, _ := do(t, ts, token, http.MethodPost, "/api/tasks", map[string]string{})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestUpdateTask(t *testing.T) {
	ts, token := newTestServer(t)
	do(t, ts, token, http.MethodPost, "/api/tasks", meads.AddTaskInput{Title: "Before"})
	resp, body := do(t, ts, token, http.MethodPatch, "/api/tasks/1",
		meads.UpdateTaskInput{ID: 1, Status: "inprogress", Title: "After"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	var got meads.Task
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Status != "inprogress" || got.Title != "After" {
		t.Errorf("unexpected: %+v", got)
	}
}

func TestDeleteTask(t *testing.T) {
	ts, token := newTestServer(t)
	do(t, ts, token, http.MethodPost, "/api/tasks", meads.AddTaskInput{Title: "Doomed"})
	resp, _ := do(t, ts, token, http.MethodDelete, "/api/tasks/1", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d", resp.StatusCode)
	}
	// Should no longer be listed.
	_, body := do(t, ts, token, http.MethodGet, "/api/tasks", nil)
	var list []meads.Task
	_ = json.Unmarshal(body, &list)
	if len(list) != 0 {
		t.Errorf("expected 0 tasks after delete, got %d", len(list))
	}
}

func TestAddDependency(t *testing.T) {
	ts, token := newTestServer(t)
	do(t, ts, token, http.MethodPost, "/api/tasks", meads.AddTaskInput{Title: "Parent"})
	do(t, ts, token, http.MethodPost, "/api/tasks", meads.AddTaskInput{Title: "Child"})
	resp, body := do(t, ts, token, http.MethodPost, "/api/tasks/2/deps", map[string]int{"parent_id": 1})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	var got meads.Task
	_ = json.Unmarshal(body, &got)
	if len(got.DependsOn) != 1 || got.DependsOn[0] != 1 {
		t.Errorf("expected depends_on=[1], got %v", got.DependsOn)
	}
}

func TestRemoveDependency(t *testing.T) {
	ts, token := newTestServer(t)
	do(t, ts, token, http.MethodPost, "/api/tasks", meads.AddTaskInput{Title: "Parent"})
	do(t, ts, token, http.MethodPost, "/api/tasks", meads.AddTaskInput{Title: "Child"})
	do(t, ts, token, http.MethodPost, "/api/tasks/2/deps", map[string]int{"parent_id": 1})

	resp, body := do(t, ts, token, http.MethodDelete, "/api/tasks/2/deps/1", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	var got meads.Task
	_ = json.Unmarshal(body, &got)
	if len(got.DependsOn) != 0 {
		t.Errorf("expected depends_on=[], got %v", got.DependsOn)
	}

	// Removing an absent parent is accepted and leaves deps empty.
	resp, _ = do(t, ts, token, http.MethodDelete, "/api/tasks/2/deps/1", nil)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200 for absent-parent remove, got %d", resp.StatusCode)
	}

	// Invalid parent IDs reject with 400.
	resp, _ = do(t, ts, token, http.MethodDelete, "/api/tasks/2/deps/0", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for zero parent, got %d", resp.StatusCode)
	}
	resp, _ = do(t, ts, token, http.MethodDelete, "/api/tasks/2/deps/abc", nil)
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for non-numeric parent, got %d", resp.StatusCode)
	}
}

func TestUpdateTask_Type(t *testing.T) {
	ts, token := newTestServer(t)
	do(t, ts, token, http.MethodPost, "/api/tasks", meads.AddTaskInput{Title: "Triage"})

	// Valid type updates.
	resp, body := do(t, ts, token, http.MethodPatch, "/api/tasks/1",
		meads.UpdateTaskInput{ID: 1, Type: "bug"})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	var got meads.Task
	_ = json.Unmarshal(body, &got)
	if got.Type != "bug" {
		t.Errorf("expected type=bug, got %q", got.Type)
	}

	// Invalid type rejects.
	resp, _ = do(t, ts, token, http.MethodPatch, "/api/tasks/1",
		meads.UpdateTaskInput{ID: 1, Type: "nonsense"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400 for invalid type, got %d", resp.StatusCode)
	}
}

func TestReady_FiltersBlocked(t *testing.T) {
	ts, token := newTestServer(t)
	do(t, ts, token, http.MethodPost, "/api/tasks", meads.AddTaskInput{Title: "Blocker"})
	do(t, ts, token, http.MethodPost, "/api/tasks", meads.AddTaskInput{Title: "Blocked"})
	do(t, ts, token, http.MethodPost, "/api/tasks/2/deps", map[string]int{"parent_id": 1})
	_, body := do(t, ts, token, http.MethodGet, "/api/ready", nil)
	var ready []meads.Task
	_ = json.Unmarshal(body, &ready)
	if len(ready) != 1 || ready[0].ID != 1 {
		t.Errorf("ready should be [1], got %+v", ready)
	}
}

func TestFileInfo(t *testing.T) {
	ts, token := newTestServer(t)
	do(t, ts, token, http.MethodPost, "/api/tasks", meads.AddTaskInput{Title: "Hello"})
	_, body := do(t, ts, token, http.MethodGet, "/api/file", nil)
	var fi fileInfo
	if err := json.Unmarshal(body, &fi); err != nil {
		t.Fatalf("decode: %v body=%s", err, body)
	}
	if fi.TaskCount != 1 {
		t.Errorf("expected task_count=1, got %d", fi.TaskCount)
	}
	if fi.Format != "md" {
		t.Errorf("expected format=md, got %s", fi.Format)
	}
}

func TestOrigin_RejectsNonLoopback(t *testing.T) {
	ts, token := newTestServer(t)
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/tasks", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Origin", "https://evil.example.com")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("expected 403 for non-loopback origin, got %d", resp.StatusCode)
	}
}

// TestTags_AddUpdateClear covers the tags wiring on the HTTP API: both
// accepted input forms, the validation boundary, and the pointer that lets
// a present-but-empty "tags" clear them.
func TestTags_AddUpdateClear(t *testing.T) {
	ts, token := newTestServer(t)
	resp, body := do(t, ts, token, http.MethodPost, "/api/tasks",
		meads.AddTaskInput{Title: "Tagged", Tags: meads.Tags{"API", "web-ui"}})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status=%d body=%s", resp.StatusCode, body)
	}
	if got := fetchTags(t, ts, token, 1); got != "api,web-ui" {
		t.Errorf("tags after add = %q, want api,web-ui", got)
	}

	// The CSV form is accepted too - Tags.UnmarshalJSON takes either.
	resp, body = do(t, ts, token, http.MethodPatch, "/api/tasks/1",
		json.RawMessage(`{"tags":"docs, api"}`))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch status=%d body=%s", resp.StatusCode, body)
	}
	if got := fetchTags(t, ts, token, 1); got != "docs,api" {
		t.Errorf("tags after patch = %q, want docs,api", got)
	}

	// An invalid tag is a 400, and changes nothing.
	resp, body = do(t, ts, token, http.MethodPatch, "/api/tasks/1",
		json.RawMessage(`{"tags":["web ui"]}`))
	if resp.StatusCode != http.StatusBadRequest {
		t.Fatalf("patch (invalid) status=%d body=%s", resp.StatusCode, body)
	}
	if got := fetchTags(t, ts, token, 1); got != "docs,api" {
		t.Errorf("tags after rejected patch = %q, want docs,api", got)
	}

	// An omitted "tags" leaves them alone; an empty one clears them.
	if _, body = do(t, ts, token, http.MethodPatch, "/api/tasks/1",
		json.RawMessage(`{"title":"Renamed"}`)); len(body) == 0 {
		t.Fatal("empty patch response")
	}
	if got := fetchTags(t, ts, token, 1); got != "docs,api" {
		t.Errorf("tags after unrelated patch = %q, want docs,api", got)
	}
	resp, body = do(t, ts, token, http.MethodPatch, "/api/tasks/1", json.RawMessage(`{"tags":[]}`))
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch (clear) status=%d body=%s", resp.StatusCode, body)
	}
	if got := fetchTags(t, ts, token, 1); got != "" {
		t.Errorf("tags after clear = %q, want none", got)
	}
}

func fetchTags(t *testing.T, ts *httptest.Server, token string, id int) string {
	t.Helper()
	resp, body := do(t, ts, token, http.MethodGet, "/api/tasks/"+strconv.Itoa(id), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("get status=%d body=%s", resp.StatusCode, body)
	}
	var got meads.Task
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v body=%s", err, body)
	}
	return got.Tags.String()
}
