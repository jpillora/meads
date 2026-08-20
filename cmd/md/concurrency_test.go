package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestConcurrentAdd_Processes is task 68's acceptance check, and it has to be
// a process-level test. The in-process one (pkg/meads's
// TestConcurrentAdds_AllLand) shares a *Store, so it exercises the file lock
// but not the thing that actually broke: separate `md` processes each with
// their own view of the file, which is how several agents really use this.
// That gap is why the bug stayed open through two rounds of in-process tests.
func TestConcurrentAdd_Processes(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping binary spawn test in short mode")
	}
	bin := filepath.Join(t.TempDir(), "md")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	h := newHarness(t)
	h.addTask("Seed") // ensure the file exists with a preamble

	const n = 25
	var wg sync.WaitGroup
	out := make([]string, n)
	errs := make([]error, n)
	wg.Add(n)
	for i := range n {
		go func() {
			defer wg.Done()
			cmd := exec.Command(bin, "--tasks-file", h.globals.TasksFile,
				"add", fmt.Sprintf("Concurrent task %d", i))
			cmd.Dir = h.dir
			b, err := cmd.CombinedOutput()
			out[i], errs[i] = string(b), err
		}()
	}
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Errorf("md add %d failed: %v\n%s", i, err, out[i])
		}
	}

	tasks, err := h.store.GetAll()
	if err != nil {
		t.Fatal(err)
	}
	// Titles, not just a count: before the atomic release in lock.go, a writer
	// could win the lock while the previous holder was mid-rewrite, read back
	// an empty file and commit a task list with everyone else's work missing.
	// A count check alone would still have caught that, but only sometimes -
	// the ids collide too, and duplicate ids are what a stale read leaves
	// behind.
	seen := map[string]int{}
	ids := map[int]int{}
	for _, task := range tasks {
		seen[task.Title]++
		ids[task.ID]++
	}
	for i := range n {
		title := fmt.Sprintf("Concurrent task %d", i)
		if seen[title] != 1 {
			t.Errorf("%q landed %d times, want 1", title, seen[title])
		}
	}
	for id, count := range ids {
		if count != 1 {
			t.Errorf("id %d assigned to %d tasks", id, count)
		}
	}
	if len(tasks) != n+1 {
		t.Errorf("expected %d tasks (seed + %d), got %d", n+1, n, len(tasks))
	}

	// A crash between the temp write and the rename would leave scratch files
	// behind, and they would show up as untracked noise in git status.
	entries, err := filepath.Glob(filepath.Join(h.dir, ".TASKS.md.tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("atomicWrite left temp files behind: %v", entries)
	}
}

// TestConcurrentAdd_ExitStatusMatchesFile guards the invariant that makes the
// exit code trustworthy: a task landed if and only if `md add` exited 0. A
// task appearing after a failure, or missing after a success, is the silent
// corruption the atomic release in lock.go exists to prevent.
//
// The contention branch below is a guard, not the point: at this width no
// writer exhausts the retry budget, so it rarely fires.
// TestAdd_HeldLockReportsContention covers the give-up path deterministically.
func TestConcurrentAdd_ExitStatusMatchesFile(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping binary spawn test in short mode")
	}
	bin := filepath.Join(t.TempDir(), "md")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	h := newHarness(t)
	h.addTask("Seed")

	const n = 10
	var wg sync.WaitGroup
	results := make([]struct {
		out string
		err error
	}, n)
	wg.Add(n)
	for i := range n {
		go func() {
			defer wg.Done()
			cmd := exec.Command(bin, "--tasks-file", h.globals.TasksFile,
				"add", fmt.Sprintf("Task %d", i))
			cmd.Dir = h.dir
			b, err := cmd.CombinedOutput()
			results[i].out, results[i].err = string(b), err
		}()
	}
	wg.Wait()

	tasks, err := h.store.GetAll()
	if err != nil {
		t.Fatal(err)
	}
	landed := map[string]bool{}
	for _, task := range tasks {
		landed[task.Title] = true
	}
	for i := range n {
		title := fmt.Sprintf("Task %d", i)
		failed := results[i].err != nil
		if failed && !strings.Contains(results[i].out, "lock contention") {
			t.Errorf("add %d failed for a reason other than contention: %v\n%s", i, results[i].err, results[i].out)
		}
		// The invariant that matters: exit status and the file agree. A task
		// landing after a non-zero exit, or missing after a zero exit, is the
		// silent corruption this test exists to rule out.
		if failed == landed[title] {
			t.Errorf("add %d: exited with err=%v but landed=%v\n%s", i, results[i].err, landed[title], results[i].out)
		}
	}
}

// TestAdd_HeldLockReportsContention exercises the other end of the retry
// budget deterministically: a lock line nobody ever releases. The retries all
// lose, and `md add` must then exit non-zero saying so - never exit 0 having
// written nothing, and never write anything.
func TestAdd_HeldLockReportsContention(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping binary spawn test in short mode")
	}
	bin := filepath.Join(t.TempDir(), "md")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}

	h := newHarness(t)
	h.addTask("Seed")

	// A lock line dated now, so it is well inside the 60s expiry the whole
	// time this runs, and owned by an id no writer here can match.
	before, err := os.ReadFile(h.globals.TasksFile)
	if err != nil {
		t.Fatal(err)
	}
	held := string(before) + fmt.Sprintf("\nlock:deadbeefdeadbeefdeadbeefdeadbeef:%d\n", time.Now().Unix())
	if err := os.WriteFile(h.globals.TasksFile, []byte(held), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(bin, "--tasks-file", h.globals.TasksFile, "add", "Should not land")
	cmd.Dir = h.dir
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("md add should have failed against a held lock; output: %s", out)
	}
	if !strings.Contains(string(out), "lock contention") {
		t.Errorf("error should name lock contention, got: %s", out)
	}
	after, err := os.ReadFile(h.globals.TasksFile)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(after), "Should not land") {
		t.Error("a writer that reported contention still wrote to the file")
	}
	// The holder's line must survive: stripping it is releasing a lock we
	// never held.
	if !strings.Contains(string(after), "lock:deadbeefdeadbeefdeadbeefdeadbeef:") {
		t.Errorf("the holder's lock line was removed by a writer that lost:\n%s", after)
	}
}
