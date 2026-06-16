package meads

import (
	"strconv"
	"strings"
)

// depGraph builds the dependency adjacency map of active (non-deleted) tasks.
// ids is the set of active task IDs; adj maps each task to its DependsOn list.
func depGraph(f *File) (ids map[int]bool, adj map[int][]int) {
	ids = make(map[int]bool, len(f.Tasks))
	adj = make(map[int][]int, len(f.Tasks))
	for _, t := range f.Tasks {
		if t.Deleted {
			continue
		}
		ids[t.ID] = true
		adj[t.ID] = t.DependsOn
	}
	return ids, adj
}

// findCycle returns a single dependency cycle as a path start → … → start
// (the first and last elements are equal), or nil if the graph is acyclic.
// Detection is a depth-first search that reports the first back-edge found.
func findCycle(ids map[int]bool, adj map[int][]int) []int {
	const (
		unvisited = 0
		inPath    = 1
		done      = 2
	)
	state := make(map[int]int, len(ids))
	parent := make(map[int]int, len(ids))
	var start int
	found := false
	var dfs func(id int)
	dfs = func(id int) {
		state[id] = inPath
		for _, dep := range adj[id] {
			if !ids[dep] {
				continue // dangling refs are validated separately
			}
			switch state[dep] {
			case inPath:
				// Back-edge: dep is an ancestor on the current path.
				parent[dep] = id
				start = dep
				found = true
				return
			case unvisited:
				parent[dep] = id
				dfs(dep)
				if found {
					return
				}
			}
		}
		state[id] = done
	}
	for id := range ids {
		if state[id] == unvisited {
			dfs(id)
			if found {
				break
			}
		}
	}
	if !found {
		return nil
	}
	// Reconstruct the cycle by walking parents from start back to start.
	path := []int{start}
	for cur := parent[start]; cur != start; cur = parent[cur] {
		path = append(path, cur)
	}
	path = append(path, start)
	// Reverse so it reads start → … → start in dependency order.
	for i, j := 0, len(path)-1; i < j; i, j = i+1, j-1 {
		path[i], path[j] = path[j], path[i]
	}
	return path
}

// findCycles returns one representative cycle for each independent dependency
// cycle in the graph. It repeatedly finds a cycle and prunes one of its edges
// until the graph is acyclic, so every cyclic cluster is reported at least
// once. The input adjacency map is not modified.
func findCycles(ids map[int]bool, adj map[int][]int) [][]int {
	// Copy adjacency so edge pruning doesn't mutate the caller's slices.
	work := make(map[int][]int, len(adj))
	for id, deps := range adj {
		work[id] = append([]int(nil), deps...)
	}
	var cycles [][]int
	for {
		cycle := findCycle(ids, work)
		if cycle == nil {
			break
		}
		cycles = append(cycles, cycle)
		// Prune the first edge of the cycle (cycle[0] → cycle[1]) to guarantee
		// progress; the loop terminates once the graph becomes acyclic.
		from, to := cycle[0], cycle[1]
		pruned := work[from][:0:0]
		removed := false
		for _, d := range work[from] {
			if !removed && d == to {
				removed = true
				continue
			}
			pruned = append(pruned, d)
		}
		work[from] = pruned
	}
	return cycles
}

// FormatCycle renders a cycle path as "a → b → … → a".
func FormatCycle(path []int) string {
	parts := make([]string, len(path))
	for i, id := range path {
		parts[i] = strconv.Itoa(id)
	}
	return strings.Join(parts, " → ")
}
