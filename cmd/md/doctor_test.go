package main

import (
	"os"
	"strings"
	"testing"
)

// captureStdout runs fn with os.Stdout redirected and returns what it printed.
func captureStdout(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	old := os.Stdout
	os.Stdout = w
	runErr := fn()
	w.Close()
	os.Stdout = old
	buf := make([]byte, 0, 1024)
	tmp := make([]byte, 512)
	for {
		n, readErr := r.Read(tmp)
		buf = append(buf, tmp[:n]...)
		if readErr != nil {
			break
		}
	}
	return string(buf), runErr
}

// TestDoctor_DetectsExistingCycle covers the "warn on existing circular graphs"
// case: a cycle that prevention can't catch because each edit was valid alone
// (here simulated by editing the file directly, as a git merge would produce).
func TestDoctor_DetectsExistingCycle(t *testing.T) {
	h := newHarness(t)
	h.addTask("A") // id 1
	h.addTask("B") // id 2
	h.addDep(2, 1) // 2 → 1 (valid)

	// Inject the back-edge 1 → 2 directly, forming a 1 ↔ 2 cycle that no single
	// md command could have created. The first status line belongs to task 1.
	content := h.tasksFileContent()
	content = strings.Replace(content, "* status: open\n", "* status: open\n* depends-on: 2\n", 1)
	if err := os.WriteFile(h.globals.TasksFile, []byte(content), 0644); err != nil {
		t.Fatalf("inject cycle: %v", err)
	}

	out, err := captureStdout(t, (&doctorCmd{globals: h.globals}).Run)
	if err == nil {
		t.Fatal("doctor should exit non-zero when an unfixable cycle remains")
	}
	if !strings.Contains(err.Error(), "circular") {
		t.Errorf("error should mention the circular dependency, got %v", err)
	}
	if !strings.Contains(out, "Circular dependency detected") {
		t.Errorf("doctor should report the cycle on stdout, got %q", out)
	}
	if !strings.Contains(out, "1") || !strings.Contains(out, "2") {
		t.Errorf("reported cycle should name tasks 1 and 2, got %q", out)
	}
}

func TestDoctor_NoCycle_NoIssues(t *testing.T) {
	h := newHarness(t)
	h.addTask("A") // id 1
	h.addTask("B") // id 2
	h.addDep(2, 1) // valid chain, no cycle

	out, err := captureStdout(t, (&doctorCmd{globals: h.globals}).Run)
	if err != nil {
		t.Fatalf("doctor on a clean file should not error, got %v", err)
	}
	if !strings.Contains(out, "no issues found") {
		t.Errorf("expected 'no issues found', got %q", out)
	}
}
