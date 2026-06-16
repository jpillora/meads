package meads

import (
	"sort"
	"strconv"
	"strings"
	"testing"
)

// cycleSig returns an order-independent signature of a cycle's distinct nodes,
// so assertions don't depend on which node DFS happens to start from.
func cycleSig(cycle []int) string {
	seen := map[int]bool{}
	var nodes []int
	for _, id := range cycle {
		if !seen[id] {
			seen[id] = true
			nodes = append(nodes, id)
		}
	}
	sort.Ints(nodes)
	parts := make([]string, len(nodes))
	for i, n := range nodes {
		parts[i] = strconv.Itoa(n)
	}
	return strings.Join(parts, ",")
}

func sigSet(cycles [][]int) map[string]bool {
	out := map[string]bool{}
	for _, c := range cycles {
		out[cycleSig(c)] = true
	}
	return out
}

func TestFindCycle_Path_StartsAndEndsSame(t *testing.T) {
	f := &File{Tasks: []Task{
		{ID: 1, DependsOn: []int{2}},
		{ID: 2, DependsOn: []int{3}},
		{ID: 3, DependsOn: []int{1}},
	}}
	ids, adj := depGraph(f)
	cycle := findCycle(ids, adj)
	if len(cycle) != 4 {
		t.Fatalf("expected a 3-node cycle path of length 4, got %v", cycle)
	}
	if cycle[0] != cycle[len(cycle)-1] {
		t.Errorf("cycle path should start and end at the same node: %v", cycle)
	}
	if cycleSig(cycle) != "1,2,3" {
		t.Errorf("cycle should span nodes 1,2,3, got %v", cycle)
	}
}

func TestFindCycles_None(t *testing.T) {
	f := &File{Tasks: []Task{
		{ID: 1, DependsOn: []int{2, 3}},
		{ID: 2, DependsOn: []int{4}},
		{ID: 3, DependsOn: []int{4}},
		{ID: 4},
	}}
	ids, adj := depGraph(f)
	if cycles := findCycles(ids, adj); len(cycles) != 0 {
		t.Errorf("expected no cycles in a diamond, got %v", cycles)
	}
}

func TestFindCycles_SelfLoop(t *testing.T) {
	f := &File{Tasks: []Task{{ID: 7, DependsOn: []int{7}}}}
	ids, adj := depGraph(f)
	cycles := findCycles(ids, adj)
	if len(cycles) != 1 {
		t.Fatalf("expected 1 self-loop cycle, got %v", cycles)
	}
	if cycleSig(cycles[0]) != "7" {
		t.Errorf("self-loop should span node 7, got %v", cycles[0])
	}
}

func TestFindCycles_TwoIndependent(t *testing.T) {
	// Two disjoint 2-cycles: 1↔2 and 3↔4.
	f := &File{Tasks: []Task{
		{ID: 1, DependsOn: []int{2}},
		{ID: 2, DependsOn: []int{1}},
		{ID: 3, DependsOn: []int{4}},
		{ID: 4, DependsOn: []int{3}},
	}}
	ids, adj := depGraph(f)
	cycles := findCycles(ids, adj)
	if len(cycles) != 2 {
		t.Fatalf("expected 2 independent cycles, got %d: %v", len(cycles), cycles)
	}
	got := sigSet(cycles)
	for _, want := range []string{"1,2", "3,4"} {
		if !got[want] {
			t.Errorf("expected a cycle spanning {%s}, cycles=%v", want, cycles)
		}
	}
}

func TestFindCycles_DoesNotMutateInput(t *testing.T) {
	f := &File{Tasks: []Task{
		{ID: 1, DependsOn: []int{2}},
		{ID: 2, DependsOn: []int{1}},
	}}
	ids, adj := depGraph(f)
	_ = findCycles(ids, adj)
	if len(adj[1]) != 1 || adj[1][0] != 2 {
		t.Errorf("findCycles must not mutate the adjacency map; adj[1]=%v", adj[1])
	}
}

func TestFindCycles_SkipsDeleted(t *testing.T) {
	// The back-edge runs through a deleted task, so there is no live cycle.
	f := &File{Tasks: []Task{
		{ID: 1, DependsOn: []int{2}},
		{ID: 2, Deleted: true, DependsOn: []int{1}},
	}}
	ids, adj := depGraph(f)
	if cycles := findCycles(ids, adj); len(cycles) != 0 {
		t.Errorf("expected no cycles when a deleted task breaks the loop, got %v", cycles)
	}
}

func TestFormatCycle(t *testing.T) {
	if got := FormatCycle([]int{1, 2, 1}); got != "1 → 2 → 1" {
		t.Errorf("FormatCycle = %q, want %q", got, "1 → 2 → 1")
	}
}

func TestStoreFindCycles_ReadPath(t *testing.T) {
	content := "# TASKS\n\n" +
		"## 1. A\n\n* status: open\n* depends-on: 2\n\n" +
		"## 2. B\n\n* status: open\n* depends-on: 1\n"
	s := newTestStore(t, content)
	cycles, err := s.FindCycles()
	if err != nil {
		t.Fatalf("FindCycles: %v", err)
	}
	if len(cycles) != 1 {
		t.Fatalf("expected 1 cycle, got %d: %v", len(cycles), cycles)
	}
	if cycleSig(cycles[0]) != "1,2" {
		t.Errorf("expected cycle spanning {1,2}, got %v", cycles[0])
	}
}

func TestStoreFindCycles_MissingFile(t *testing.T) {
	s := newTestStore(t, "") // no file written
	cycles, err := s.FindCycles()
	if err != nil {
		t.Fatalf("FindCycles on missing file should not error, got %v", err)
	}
	if len(cycles) != 0 {
		t.Errorf("expected no cycles for missing file, got %v", cycles)
	}
}
