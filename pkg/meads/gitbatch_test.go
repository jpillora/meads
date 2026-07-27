package meads

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// Tests for the batched task read (RefStore.ReadFilesAtCommits, task 73):
// a git-mode read must cost a constant number of git processes rather than
// one pair per task.

// invocationCounter counts EVERY git invocation made through it, whichever
// entry point it arrives by - the claim under test is about processes
// spawned, so it must not miss a category the way a per-method counter can.
type invocationCounter struct {
	Git
	mu   sync.Mutex
	args [][]string
}

func (c *invocationCounter) note(args []string) {
	c.mu.Lock()
	c.args = append(c.args, args)
	c.mu.Unlock()
}

func (c *invocationCounter) Run(args ...string) error {
	c.note(args)
	return c.Git.Run(args...)
}

func (c *invocationCounter) Output(args ...string) (string, error) {
	c.note(args)
	return c.Git.Output(args...)
}

func (c *invocationCounter) OutputWithInput(stdin string, args ...string) (string, error) {
	c.note(args)
	return c.Git.OutputWithInput(stdin, args...)
}

func (c *invocationCounter) OutputRaw(args ...string) ([]byte, error) {
	c.note(args)
	return c.Git.OutputRaw(args...)
}

func (c *invocationCounter) OutputRawWithInput(stdin string, args ...string) ([]byte, error) {
	c.note(args)
	return c.Git.OutputRawWithInput(stdin, args...)
}

func (c *invocationCounter) reset() {
	c.mu.Lock()
	c.args = nil
	c.mu.Unlock()
}

func (c *invocationCounter) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.args)
}

func (c *invocationCounter) describe() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	var b strings.Builder
	for _, a := range c.args {
		fmt.Fprintf(&b, "\n  git %s", strings.Join(a, " "))
	}
	return b.String()
}

// TestGitStore_LoadAll_ProcessCountDoesNotScale is task 73's acceptance
// criterion: the git invocations a read costs must not grow with the number
// of tasks. Every task is its own ref, so the natural shape is a ref
// resolve plus a blob read PER TASK, which made a 100-task `md list` spawn
// 403 processes and spend most of its wall clock in fork/exec.
func TestGitStore_LoadAll_ProcessCountDoesNotScale(t *testing.T) {
	gs, _, _ := newGitStoreRepo(t)
	counter := &invocationCounter{Git: gs.git}
	gs.refs = NewRefStore(counter)

	created := 0
	// measure grows the store to total tasks, then counts the git
	// invocations one LoadAll over it costs.
	measure := func(total int) int {
		for ; created < total; created++ {
			if _, err := gs.Create(Task{Title: fmt.Sprintf("task %d", created), Status: "open"}); err != nil {
				t.Fatalf("Create: %v", err)
			}
		}
		counter.reset()
		got, err := gs.LoadAll()
		if err != nil {
			t.Fatalf("LoadAll: %v", err)
		}
		if len(got) != total {
			t.Fatalf("LoadAll returned %d tasks, want %d", len(got), total)
		}
		return counter.count()
	}

	few := measure(3)
	many := measure(33)

	if few != many {
		t.Errorf("LoadAll spawned %d git processes over 3 tasks but %d over 33: the cost scales with task count%s",
			few, many, counter.describe())
	}

	// Belt and braces on the absolute number: one for-each-ref to enumerate
	// and one cat-file --batch to read is the whole budget.
	if many > 2 {
		t.Errorf("LoadAll spawned %d git processes, want at most 2 (for-each-ref + cat-file --batch)%s", many, counter.describe())
	}
}

// TestGitStore_LoadAll_BatchMatchesPerTaskReads pins the batch's CONTENT,
// not just its process count: results are matched to inputs by position
// (the oid in each cat-file frame is the blob's, not the commit's), so an
// ordering slip would silently hand every task another task's body. Task
// ids and titles are deliberately unrelated here so a shifted mapping
// cannot look correct.
func TestGitStore_LoadAll_BatchMatchesPerTaskReads(t *testing.T) {
	gs, _, _ := newGitStoreRepo(t)
	want := map[int]string{}
	for i := 1; i <= 12; i++ {
		created, err := gs.Create(Task{Title: fmt.Sprintf("title-%d", i*7), Status: "open"})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		want[created.ID] = created.Title
	}
	// A body containing newlines: cat-file frames are length-delimited, so a
	// parser that scanned for the next LF instead of honouring <size> would
	// desynchronise here and corrupt every following task.
	if _, err := gs.Update(1, func(task *Task) (bool, error) {
		task.Description = "line one\nline two\n\nline four"
		return true, nil
	}); err != nil {
		t.Fatalf("Update: %v", err)
	}

	got, err := gs.LoadAll()
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(got) != len(want) {
		t.Fatalf("LoadAll returned %d tasks, want %d", len(got), len(want))
	}
	for _, task := range got {
		if task.Title != want[task.ID] {
			t.Errorf("task %d title = %q, want %q (batch results mapped to the wrong ids)", task.ID, task.Title, want[task.ID])
		}
	}
	if got[0].ID != 1 || got[0].Description != "line one\nline two\n\nline four" {
		t.Errorf("task 1 description = %q, want the multi-line body intact", got[0].Description)
	}
	// Ascending by id, as documented.
	for i := 1; i < len(got); i++ {
		if got[i-1].ID >= got[i].ID {
			t.Fatalf("LoadAll not ascending by id: %d before %d", got[i-1].ID, got[i].ID)
		}
	}
}

func TestParseCatFileBatch(t *testing.T) {
	tests := []struct {
		name    string
		out     string
		want    int
		expect  []string
		wantErr string
	}{
		{
			name:   "two payloads",
			out:    "aaa blob 5\nhello\nbbb blob 3\nbye\n",
			want:   2,
			expect: []string{"hello", "bye"},
		},
		{
			name:   "payload containing newlines is taken by size",
			out:    "aaa blob 11\none\ntwo\nsix\nbbb blob 2\nok\n",
			want:   2,
			expect: []string{"one\ntwo\nsix", "ok"},
		},
		{
			name:   "empty payload",
			out:    "aaa blob 0\n\nbbb blob 2\nok\n",
			want:   2,
			expect: []string{"", "ok"},
		},
		{
			name:    "missing object is an error naming the frame",
			out:     "deadbeef:task.json missing\n",
			want:    1,
			wantErr: "missing",
		},
		{
			name:    "truncated output",
			out:     "aaa blob 20\nshort\n",
			want:    1,
			wantErr: "only",
		},
		{
			name:    "fewer frames than requested",
			out:     "aaa blob 2\nok\n",
			want:    2,
			wantErr: "output ended after 1 of 2",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := parseCatFileBatch([]byte(tt.out), tt.want, "task.json")
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("err = %v, want one containing %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseCatFileBatch: %v", err)
			}
			if len(got) != len(tt.expect) {
				t.Fatalf("got %d payloads, want %d", len(got), len(tt.expect))
			}
			for i, want := range tt.expect {
				if string(got[i]) != want {
					t.Errorf("payload %d = %q, want %q", i, got[i], want)
				}
			}
		})
	}
}
