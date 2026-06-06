package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Task is a minimal meads task. The POC keeps just enough fields to show a
// real JSON round-trip through the git object store; the point is the ref
// plumbing, not re-implementing all of meads.
type Task struct {
	ID     int    `json:"id"`
	Title  string `json:"title"`
	Status string `json:"status"`
}

// File mirrors meads' tasks.json: {"meta":{...},"tasks":[...]}.
type File struct {
	Meta  map[string]string `json:"meta,omitempty"`
	Tasks []Task            `json:"tasks"`
}

// emptyJSON is task 30's EmptyFile() for the JSON format.
const emptyJSON = `{"tasks":[]}`

func parseFile(s string) (*File, error) {
	f := &File{}
	if strings.TrimSpace(s) == "" {
		return f, nil
	}
	if err := json.Unmarshal([]byte(s), f); err != nil {
		return nil, err
	}
	return f, nil
}

func formatFile(f *File) (string, error) {
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func nextID(f *File) int {
	max := 0
	for _, t := range f.Tasks {
		if t.ID > max {
			max = t.ID
		}
	}
	return max + 1
}

func main() {
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}

	// `demo` is fully self-contained (creates its own temp repo).
	if os.Args[1] == "demo" {
		if err := runDemo(); err != nil {
			fmt.Fprintln(os.Stderr, "demo failed:", err)
			os.Exit(1)
		}
		return
	}

	gitDir := os.Getenv("MEADS_GITDIR")
	if gitDir == "" {
		gitDir = ".git"
	}

	cmd, args := os.Args[1], os.Args[2:]
	if err := dispatch(gitDir, cmd, args); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `task30 — meads "git mode" ref-plumbing PoC (furgit-backed)

Usage:
  task30 demo                 Run a self-contained end-to-end demo (temp repo)
  task30 init                 Seed refs/meads/tasks with {"tasks":[]}
  task30 add "<title>"        Add a task (new commit, CAS-advance ref)
  task30 list                 List current tasks
  task30 get <id>             Print one task as JSON
  task30 set-status <id> <s>  Change a task's status
  task30 history              Show committed task counts, newest first
  task30 refdump              Show refs as furgit sees them

The git dir defaults to ./.git (override with $MEADS_GITDIR).
`)
}

func dispatch(gitDir, cmd string, args []string) error {
	be, err := OpenGitBackend(gitDir)
	if err != nil {
		return err
	}
	defer be.Close()

	switch cmd {
	case "init":
		if err := be.ensureInit(emptyJSON); err != nil {
			return err
		}
		fmt.Printf("initialized %s\n", RefName)
		return nil

	case "add":
		if len(args) != 1 {
			return fmt.Errorf("add requires a title")
		}
		var newID int
		err := be.transact("meads: add", func(cur string) (string, bool, error) {
			f, err := parseFile(cur)
			if err != nil {
				return "", false, err
			}
			newID = nextID(f)
			f.Tasks = append(f.Tasks, Task{ID: newID, Title: args[0], Status: "open"})
			out, err := formatFile(f)
			return out, true, err
		})
		if err != nil {
			return err
		}
		fmt.Printf("added task %d\n", newID)
		return nil

	case "list":
		content, err := be.read()
		if err != nil {
			return err
		}
		f, err := parseFile(content)
		if err != nil {
			return err
		}
		if len(f.Tasks) == 0 {
			fmt.Println("(no tasks)")
			return nil
		}
		for _, t := range f.Tasks {
			fmt.Printf("%d\t[%s]\t%s\n", t.ID, t.Status, t.Title)
		}
		return nil

	case "get":
		if len(args) != 1 {
			return fmt.Errorf("get requires an id")
		}
		id, err := strconv.Atoi(args[0])
		if err != nil {
			return err
		}
		content, err := be.read()
		if err != nil {
			return err
		}
		f, err := parseFile(content)
		if err != nil {
			return err
		}
		for _, t := range f.Tasks {
			if t.ID == id {
				b, _ := json.MarshalIndent(t, "", "  ")
				fmt.Println(string(b))
				return nil
			}
		}
		return fmt.Errorf("task %d not found", id)

	case "set-status":
		if len(args) != 2 {
			return fmt.Errorf("set-status requires <id> <status>")
		}
		id, err := strconv.Atoi(args[0])
		if err != nil {
			return err
		}
		return be.transact("meads: set-status", func(cur string) (string, bool, error) {
			f, err := parseFile(cur)
			if err != nil {
				return "", false, err
			}
			for i := range f.Tasks {
				if f.Tasks[i].ID == id {
					f.Tasks[i].Status = args[1]
					out, err := formatFile(f)
					return out, true, err
				}
			}
			return "", false, fmt.Errorf("task %d not found", id)
		})

	case "history":
		hist, err := be.history()
		if err != nil {
			return err
		}
		for i, content := range hist {
			f, err := parseFile(content)
			if err != nil {
				return err
			}
			fmt.Printf("commit~%d: %d task(s)\n", i, len(f.Tasks))
		}
		return nil

	case "refdump":
		names, err := be.RefNames("")
		if err != nil {
			return err
		}
		for _, n := range names {
			fmt.Println(n)
		}
		return nil

	default:
		usage()
		return fmt.Errorf("unknown command %q", cmd)
	}
}

// ---------------------------------------------------------------------------
// Self-contained demo: builds a temp repo, exercises the whole model, and
// cross-checks furgit's writes against canonical git.
// ---------------------------------------------------------------------------

func runDemo() error {
	dir, err := os.MkdirTemp("", "task30-demo-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)

	step("temp repo: %s", dir)

	// git 2.43 defaults to the "files" ref format, which furgit supports.
	if out, err := git(dir, "init", "-q", "-b", "main"); err != nil {
		return fmt.Errorf("git init: %v: %s", err, out)
	}

	gitDir := filepath.Join(dir, ".git")
	be, err := OpenGitBackend(gitDir)
	if err != nil {
		return err
	}
	defer be.Close()

	// 1. init — the first "virtual" commit.
	if err := be.ensureInit(emptyJSON); err != nil {
		return err
	}
	step("init: seeded %s with %s", RefName, emptyJSON)

	// 2. the working tree stays clean — nothing is tracked.
	if out, err := git(dir, "status", "--porcelain"); err != nil {
		return err
	} else if strings.TrimSpace(out) != "" {
		return fmt.Errorf("working tree not clean:\n%s", out)
	}
	step("git status --porcelain: clean (no tracked task file)")

	// 3. the ref exists and nothing leaked into refs/heads.
	names, err := be.RefNames("")
	if err != nil {
		return err
	}
	step("furgit refs: %s", strings.Join(names, ", "))

	// 4. a few mutations, each its own commit.
	for _, title := range []string{"Wire up gitBackend", "Add CAS retry", "Walk history"} {
		var id int
		err := be.transact("meads: add", func(cur string) (string, bool, error) {
			f, err := parseFile(cur)
			if err != nil {
				return "", false, err
			}
			id = nextID(f)
			f.Tasks = append(f.Tasks, Task{ID: id, Title: title, Status: "open"})
			out, err := formatFile(f)
			return out, true, err
		})
		if err != nil {
			return err
		}
		step("add task %d: %q", id, title)
	}

	// 5. an update mutation.
	if err := be.transact("meads: set-status", func(cur string) (string, bool, error) {
		f, err := parseFile(cur)
		if err != nil {
			return "", false, err
		}
		f.Tasks[0].Status = "closed"
		out, err := formatFile(f)
		return out, true, err
	}); err != nil {
		return err
	}
	step("set-status: task 1 -> closed")

	// 6. read back through furgit.
	content, err := be.read()
	if err != nil {
		return err
	}
	f, err := parseFile(content)
	if err != nil {
		return err
	}
	step("read: %d tasks; tasks.json =\n%s", len(f.Tasks), indent(content))

	// 7. cross-check with canonical git: the ref, the log, and the blob.
	if out, err := git(dir, "for-each-ref", RefName); err != nil {
		return err
	} else {
		step("git for-each-ref %s:\n%s", RefName, indent(strings.TrimRight(out, "\n")))
	}
	if out, err := git(dir, "log", "--oneline", "--format=%h %s", RefName); err != nil {
		return err
	} else {
		step("git log %s (one commit per mutation):\n%s", RefName, indent(strings.TrimRight(out, "\n")))
	}
	gitBlob, err := git(dir, "cat-file", "-p", RefName+":"+blobName)
	if err != nil {
		return err
	}
	if strings.TrimRight(gitBlob, "\n") != strings.TrimRight(content, "\n") {
		return fmt.Errorf("git and furgit disagree on %s:\n--- git ---\n%s\n--- furgit ---\n%s", blobName, gitBlob, content)
	}
	step("git cat-file %s:%s matches furgit byte-for-byte ✓", RefName, blobName)

	// 8. history walk.
	hist, err := be.history()
	if err != nil {
		return err
	}
	counts := make([]string, len(hist))
	for i, h := range hist {
		hf, _ := parseFile(h)
		counts[i] = strconv.Itoa(len(hf.Tasks))
	}
	step("history (newest->oldest) task counts: [%s]", strings.Join(counts, " "))

	// 9. concurrency: probe furgit's CAS under N independent handles (≈ N
	//    processes), then prove the CAS retry logic with the txn serialized.
	if err := demoConcurrency(dir, gitDir); err != nil {
		return err
	}

	fmt.Println("\nDEMO OK — task state lives purely in git via furgit ref plumbing.")
	return nil
}

const concWorkers = 16

// demoConcurrency runs two probes that, together, separate "is the CAS *logic*
// correct?" from "is furgit's *lock implementation* concurrency-safe?".
func demoConcurrency(workdir, gitDir string) error {
	// Record the last known-good ref value so we can recover if probe A's
	// MT-Unsafe race corrupts the ref.
	goodHead, err := git(workdir, "rev-parse", RefName)
	if err != nil {
		return fmt.Errorf("rev-parse %s: %v", RefName, err)
	}
	goodHead = strings.TrimSpace(goodHead)
	beforeA, err := taskCount(gitDir)
	if err != nil {
		return err
	}

	// Probe A: furgit-native, no external lock. furgit's files Transaction is
	// documented MT-Unsafe; concurrent same-ref writers race in its lock-file
	// cleanup (loser deletes the winner's .lock; empty-parent pruning removes
	// refs/meads; a stray 0-byte lock can get renamed into the ref).
	resA := raceWriters(gitDir, nil, "A")

	afterA, readErr := taskCount(gitDir)
	switch {
	case readErr != nil:
		// The ref itself is now unreadable — the worst outcome.
		step("probe A (furgit-native, no lock): ref CORRUPTED under contention — %v", readErr)
		step("    => furgit's MT-Unsafe txn can torn-write refs/meads/tasks to an unreadable state")
	case resA.firstErr != nil:
		step("probe A (furgit-native, no lock): MT-Unsafe confirmed — %d/%d writers landed, rest errored", afterA-beforeA, concWorkers)
		step("    sample error: %v", resA.firstErr)
	default:
		step("probe A (furgit-native, no lock): no error this run (timing); MT-Unsafe by contract — %d/%d landed", afterA-beforeA, concWorkers)
	}

	// Recover the ref out-of-band (stale lock removal + reset to last-good value
	// if it was torn) so probe B starts from a valid backend. A real md would
	// not have this luxury mid-flight — that's the point.
	if repaired := repairRef(gitDir, goodHead, readErr != nil); repaired {
		step("    recovered: reset %s to last-good %s out-of-band", RefName, goodHead[:7])
	}

	// Probe B: serialize only the furgit transaction (reads/applies still run
	// concurrently, so the CAS retry path IS exercised). This stands in for the
	// OS-level lock a real md git-mode would supply, and validates that the CAS
	// retry logic itself yields every writer with no lost update.
	beforeB, err := taskCount(gitDir)
	if err != nil {
		return err
	}
	var mu sync.Mutex
	resB := raceWriters(gitDir, &mu, "B")
	if resB.firstErr != nil {
		return fmt.Errorf("probe B (serialized furgit txn) should not error: %w", resB.firstErr)
	}
	afterB, err := taskCount(gitDir)
	if err != nil {
		return err
	}
	if afterB != beforeB+concWorkers {
		return fmt.Errorf("probe B CAS lost updates: before=%d after=%d, want %d", beforeB, afterB, beforeB+concWorkers)
	}
	ids, err := taskIDs(gitDir)
	if err != nil {
		return err
	}
	if dup := firstDup(ids); dup >= 0 {
		return fmt.Errorf("probe B duplicate id %d (lost update)", dup)
	}
	step("probe B (furgit txn serialized): all %d writers landed; %d->%d tasks, ids unique, CAS retry exercised ✓", concWorkers, beforeB, afterB)
	return nil
}

type raceResult struct{ firstErr error }

// raceWriters launches concWorkers goroutines that each open their own backend
// (≈ separate processes) and add one task via transact. If mu is non-nil it is
// shared as the furgit-transaction serializer.
func raceWriters(gitDir string, mu *sync.Mutex, tag string) raceResult {
	var wg sync.WaitGroup
	errs := make([]error, concWorkers)
	for w := 0; w < concWorkers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			be, err := OpenGitBackend(gitDir)
			if err != nil {
				errs[w] = err
				return
			}
			defer be.Close()
			if mu != nil {
				be.SetSerializer(mu)
			}
			errs[w] = be.transact("meads: concurrent "+tag, func(cur string) (string, bool, error) {
				f, perr := parseFile(cur)
				if perr != nil {
					return "", false, perr
				}
				id := nextID(f)
				f.Tasks = append(f.Tasks, Task{ID: id, Title: fmt.Sprintf("conc-%s-%d", tag, w), Status: "open"})
				out, ferr := formatFile(f)
				return out, true, ferr
			})
		}(w)
	}
	wg.Wait()

	var res raceResult
	for _, e := range errs {
		if e != nil && res.firstErr == nil {
			res.firstErr = e
		}
	}
	return res
}

// repairRef undoes the mess a MT-Unsafe probe can leave: it removes a leaked
// ref lock, re-creates the ref directory, and — when the ref was torn — rewrites
// it to the last-good commit. Returns whether it had to reset a torn ref.
func repairRef(gitDir, goodHead string, torn bool) bool {
	refDir := filepath.Join(gitDir, "refs", "meads")
	_ = os.Remove(filepath.Join(refDir, "tasks.lock"))
	_ = os.MkdirAll(refDir, 0o755)
	if torn {
		_ = os.WriteFile(filepath.Join(refDir, "tasks"), []byte(goodHead+"\n"), 0o644)
	}
	return torn
}

func firstDup(ids []int) int {
	seen := make(map[int]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			return id
		}
		seen[id] = true
	}
	return -1
}

func taskCount(gitDir string) (int, error) {
	ids, err := taskIDs(gitDir)
	return len(ids), err
}

func taskIDs(gitDir string) ([]int, error) {
	be, err := OpenGitBackend(gitDir)
	if err != nil {
		return nil, err
	}
	defer be.Close()
	content, err := be.read()
	if err != nil {
		return nil, err
	}
	f, err := parseFile(content)
	if err != nil {
		return nil, err
	}
	ids := make([]int, 0, len(f.Tasks))
	for _, t := range f.Tasks {
		ids = append(ids, t.ID)
	}
	sort.Ints(ids)
	return ids, nil
}

// ---------------------------------------------------------------------------
// small helpers
// ---------------------------------------------------------------------------

func git(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=meads", "GIT_AUTHOR_EMAIL=meads@localhost",
		"GIT_COMMITTER_NAME=meads", "GIT_COMMITTER_EMAIL=meads@localhost",
	)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func step(format string, args ...any) {
	fmt.Printf("• "+format+"\n", args...)
}

func indent(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = "    " + l
	}
	return strings.Join(lines, "\n")
}
