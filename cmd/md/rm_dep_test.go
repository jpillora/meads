package main

import (
	"strconv"
	"testing"
)

func TestRmDepCmd(t *testing.T) {
	h := newHarness(t)
	parent := h.addTask("Parent")
	child := h.addTask("Child")
	h.addDep(child, parent)

	if deps := h.getTask(child).DependsOn; len(deps) != 1 || deps[0] != parent {
		t.Fatalf("setup: expected depends-on=[%d], got %v", parent, deps)
	}

	cmd := &rmDepCmd{globals: h.globals, Child: strconv.Itoa(child), Parent: strconv.Itoa(parent)}
	if err := cmd.Run(); err != nil {
		t.Fatalf("rm-dep: %v", err)
	}

	if deps := h.getTask(child).DependsOn; len(deps) != 0 {
		t.Errorf("expected depends-on=[], got %v", deps)
	}
}

// Removing a dependency that isn't present is a no-op, not an error.
func TestRmDepCmd_NotPresent(t *testing.T) {
	h := newHarness(t)
	parent := h.addTask("Parent")
	child := h.addTask("Child")

	cmd := &rmDepCmd{globals: h.globals, Child: strconv.Itoa(child), Parent: strconv.Itoa(parent)}
	if err := cmd.Run(); err != nil {
		t.Fatalf("rm-dep no-op: %v", err)
	}

	if deps := h.getTask(child).DependsOn; len(deps) != 0 {
		t.Errorf("expected depends-on=[], got %v", deps)
	}
}

// Non-numeric IDs are rejected with a clear error.
func TestRmDepCmd_InvalidID(t *testing.T) {
	h := newHarness(t)
	cmd := &rmDepCmd{globals: h.globals, Child: "abc", Parent: "1"}
	if err := cmd.Run(); err == nil {
		t.Fatal("expected error for non-numeric child ID, got nil")
	}
}
