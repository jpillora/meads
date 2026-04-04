package e2e

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/jpillora/meads/pkg/meads"
)

// gitMergeConflict attempts a merge that is expected to conflict.
// It leaves the repo in a conflicted state ready for manual resolution.
func gitMergeConflict(t *testing.T, dir string, branch string) {
	t.Helper()
	cmd := exec.Command("git", "merge", branch, "--no-commit")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=test", "GIT_AUTHOR_EMAIL=test@test.com",
		"GIT_COMMITTER_NAME=test", "GIT_COMMITTER_EMAIL=test@test.com",
	)
	// Conflict is expected — ignore the error.
	cmd.Run()
}

// TestDoctor_BranchMerge_CSV tests the full workflow:
// (1) add tasks on main, (2) branch to B and C, (3) add conflicting tasks
// with deps on both branches, (4) merge back, (5) run doctor, (6) confirm.
func TestDoctor_BranchMerge_CSV(t *testing.T) {
	dir := t.TempDir()
	header := meads.InitCSV()

	// --- Step 1: Initialize repo with a base task on main ---
	gitInDir(t, dir, "init", "-b", "main")
	base := header +
		"1,Base task,open,P1,task,,,,,,,,{}\n"
	os.WriteFile(filepath.Join(dir, "TASKS.csv"), []byte(base), 0644)
	gitInDir(t, dir, "add", "TASKS.csv")
	gitInDir(t, dir, "commit", "-m", "initial")

	// --- Step 2: Create branch-b and branch-c from main ---

	// Branch B: add tasks 2 and 3, where 3 depends on 2.
	gitInDir(t, dir, "checkout", "-b", "branch-b")
	branchB := header +
		"1,Base task,open,P1,task,,,,,,,,{}\n" +
		"2,Branch B feature,open,P2,feature,,,,,,,,{}\n" +
		"3,Branch B tests,open,P2,task,2,,,,,,,{}\n"
	os.WriteFile(filepath.Join(dir, "TASKS.csv"), []byte(branchB), 0644)
	gitInDir(t, dir, "add", "TASKS.csv")
	gitInDir(t, dir, "commit", "-m", "branch-b tasks")

	// Branch C: add tasks 2 and 3, where 3 depends on 2.
	gitInDir(t, dir, "checkout", "main")
	gitInDir(t, dir, "checkout", "-b", "branch-c")
	branchC := header +
		"1,Base task,open,P1,task,,,,,,,,{}\n" +
		"2,Branch C bugfix,open,P1,bug,,,,,,,,{}\n" +
		"3,Branch C validation,open,P2,task,2,,,,,,,{}\n"
	os.WriteFile(filepath.Join(dir, "TASKS.csv"), []byte(branchC), 0644)
	gitInDir(t, dir, "add", "TASKS.csv")
	gitInDir(t, dir, "commit", "-m", "branch-c tasks")

	// --- Step 3: Merge both branches back into main ---
	gitInDir(t, dir, "checkout", "main")
	gitInDir(t, dir, "merge", "branch-b")
	// branch-c will conflict on the same rows; resolve by concatenating both.
	gitMergeConflict(t, dir, "branch-c")

	// Manually resolve: keep all rows from both branches.
	merged := header +
		"1,Base task,open,P1,task,,,,,,,,{}\n" +
		"2,Branch B feature,open,P2,feature,,,,,,,,{}\n" +
		"3,Branch B tests,open,P2,task,2,,,,,,,{}\n" +
		"2,Branch C bugfix,open,P1,bug,,,,,,,,{}\n" +
		"3,Branch C validation,open,P2,task,2,,,,,,,{}\n"
	os.WriteFile(filepath.Join(dir, "TASKS.csv"), []byte(merged), 0644)
	gitInDir(t, dir, "add", "TASKS.csv")
	gitInDir(t, dir, "commit", "-m", "resolve merge")

	// --- Step 4: Run doctor on the merged file ---
	s := meads.NewFileStore(filepath.Join(dir, "TASKS.csv"))
	fixes, err := s.Doctor()
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}

	// --- Step 5: Verify results ---
	// Expect 2 fixes: duplicate ID 2 and duplicate ID 3.
	if len(fixes) != 2 {
		t.Fatalf("expected 2 fixes, got %d: %+v", len(fixes), fixes)
	}

	tasks, err := s.Get(nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(tasks) != 5 {
		t.Fatalf("expected 5 tasks, got %d", len(tasks))
	}

	// All IDs must be unique.
	ids := map[int]bool{}
	for _, task := range tasks {
		if ids[task.ID] {
			t.Errorf("duplicate ID %d after Doctor", task.ID)
		}
		ids[task.ID] = true
	}

	// Build lookup by title.
	byTitle := map[string]meads.Task{}
	for _, task := range tasks {
		byTitle[task.Title] = task
	}

	// Branch B tasks keep original IDs (first occurrence).
	bFeature := byTitle["Branch B feature"]
	bTests := byTitle["Branch B tests"]
	if bFeature.ID != 2 {
		t.Errorf("Branch B feature ID = %d, want 2", bFeature.ID)
	}
	if bTests.ID != 3 {
		t.Errorf("Branch B tests ID = %d, want 3", bTests.ID)
	}
	// Branch B tests still depends on Branch B feature (unchanged).
	if len(bTests.DependsOn) != 1 || bTests.DependsOn[0] != 2 {
		t.Errorf("Branch B tests DependsOn = %v, want [2]", bTests.DependsOn)
	}

	// Branch C tasks were renumbered.
	cBugfix := byTitle["Branch C bugfix"]
	cValidation := byTitle["Branch C validation"]
	if cBugfix.ID == 2 {
		t.Error("Branch C bugfix should have been renumbered from 2")
	}
	if cValidation.ID == 3 {
		t.Error("Branch C validation should have been renumbered from 3")
	}
	// Branch C validation should depend on Branch C bugfix's new ID.
	if len(cValidation.DependsOn) != 1 || cValidation.DependsOn[0] != cBugfix.ID {
		t.Errorf("Branch C validation DependsOn = %v, want [%d]", cValidation.DependsOn, cBugfix.ID)
	}
}

// TestDoctor_BranchMerge_Markdown tests the same workflow with Markdown format.
func TestDoctor_BranchMerge_Markdown(t *testing.T) {
	dir := t.TempDir()

	gitInDir(t, dir, "init", "-b", "main")
	base := "# TASKS\n\n## 1. Base task\n\n* status: open\n"
	os.WriteFile(filepath.Join(dir, "TASKS.md"), []byte(base), 0644)
	gitInDir(t, dir, "add", "TASKS.md")
	gitInDir(t, dir, "commit", "-m", "initial")

	// Branch B: add task 2 and task 3 (depends on 2).
	gitInDir(t, dir, "checkout", "-b", "branch-b")
	branchB := "# TASKS\n\n## 1. Base task\n\n* status: open\n\n## 2. Branch B API\n\n* status: open\n\n## 3. Branch B API tests\n\n* status: open\n* depends-on: 2\n"
	os.WriteFile(filepath.Join(dir, "TASKS.md"), []byte(branchB), 0644)
	gitInDir(t, dir, "add", "TASKS.md")
	gitInDir(t, dir, "commit", "-m", "branch-b tasks")

	// Branch C: add task 2 and task 3 (depends on 2).
	gitInDir(t, dir, "checkout", "main")
	gitInDir(t, dir, "checkout", "-b", "branch-c")
	branchC := "# TASKS\n\n## 1. Base task\n\n* status: open\n\n## 2. Branch C auth\n\n* status: open\n\n## 3. Branch C auth tests\n\n* status: open\n* depends-on: 2\n"
	os.WriteFile(filepath.Join(dir, "TASKS.md"), []byte(branchC), 0644)
	gitInDir(t, dir, "add", "TASKS.md")
	gitInDir(t, dir, "commit", "-m", "branch-c tasks")

	// Merge and manually resolve.
	gitInDir(t, dir, "checkout", "main")
	gitInDir(t, dir, "merge", "branch-b")
	gitMergeConflict(t, dir, "branch-c")

	merged := "# TASKS\n\n## 1. Base task\n\n* status: open\n\n## 2. Branch B API\n\n* status: open\n\n## 3. Branch B API tests\n\n* status: open\n* depends-on: 2\n\n## 2. Branch C auth\n\n* status: open\n\n## 3. Branch C auth tests\n\n* status: open\n* depends-on: 2\n"
	os.WriteFile(filepath.Join(dir, "TASKS.md"), []byte(merged), 0644)
	gitInDir(t, dir, "add", "TASKS.md")
	gitInDir(t, dir, "commit", "-m", "resolve merge")

	// Run doctor.
	s := meads.NewFileStore(filepath.Join(dir, "TASKS.md"))
	fixes, err := s.Doctor()
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if len(fixes) != 2 {
		t.Fatalf("expected 2 fixes, got %d: %+v", len(fixes), fixes)
	}

	tasks, err := s.Get(nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(tasks) != 5 {
		t.Fatalf("expected 5 tasks, got %d", len(tasks))
	}

	byTitle := map[string]meads.Task{}
	for _, task := range tasks {
		byTitle[task.Title] = task
	}

	// Branch B keeps originals.
	bAPI := byTitle["Branch B API"]
	bTests := byTitle["Branch B API tests"]
	if bAPI.ID != 2 {
		t.Errorf("Branch B API ID = %d, want 2", bAPI.ID)
	}
	if len(bTests.DependsOn) != 1 || bTests.DependsOn[0] != 2 {
		t.Errorf("Branch B API tests DependsOn = %v, want [2]", bTests.DependsOn)
	}

	// Branch C renumbered, depends-on updated.
	cAuth := byTitle["Branch C auth"]
	cTests := byTitle["Branch C auth tests"]
	if cAuth.ID == 2 {
		t.Error("Branch C auth should have been renumbered from 2")
	}
	if len(cTests.DependsOn) != 1 || cTests.DependsOn[0] != cAuth.ID {
		t.Errorf("Branch C auth tests DependsOn = %v, want [%d]", cTests.DependsOn, cAuth.ID)
	}
}

// TestDoctor_ThreeBranches tests doctor with three branches all creating
// the same task IDs with dependencies.
func TestDoctor_ThreeBranches(t *testing.T) {
	dir := t.TempDir()
	header := meads.InitCSV()

	gitInDir(t, dir, "init", "-b", "main")
	os.WriteFile(filepath.Join(dir, "TASKS.csv"), []byte(header), 0644)
	gitInDir(t, dir, "add", "TASKS.csv")
	gitInDir(t, dir, "commit", "-m", "initial")

	// All three branches create task 1 and task 2 (depends on 1).
	for _, branch := range []struct {
		name, t1Title, t2Title string
	}{
		{"branch-a", "A parent", "A child"},
		{"branch-b", "B parent", "B child"},
		{"branch-c", "C parent", "C child"},
	} {
		gitInDir(t, dir, "checkout", "main")
		gitInDir(t, dir, "checkout", "-b", branch.name)
		content := header +
			"1," + branch.t1Title + ",open,P2,task,,,,,,,,{}\n" +
			"2," + branch.t2Title + ",open,P2,task,1,,,,,,,{}\n"
		os.WriteFile(filepath.Join(dir, "TASKS.csv"), []byte(content), 0644)
		gitInDir(t, dir, "add", "TASKS.csv")
		gitInDir(t, dir, "commit", "-m", branch.name+" tasks")
	}

	// Merge all into main, resolving by concatenating.
	gitInDir(t, dir, "checkout", "main")
	gitInDir(t, dir, "merge", "branch-a")
	gitMergeConflict(t, dir, "branch-b")
	mergedAB := header +
		"1,A parent,open,P2,task,,,,,,,,{}\n" +
		"2,A child,open,P2,task,1,,,,,,,{}\n" +
		"1,B parent,open,P2,task,,,,,,,,{}\n" +
		"2,B child,open,P2,task,1,,,,,,,{}\n"
	os.WriteFile(filepath.Join(dir, "TASKS.csv"), []byte(mergedAB), 0644)
	gitInDir(t, dir, "add", "TASKS.csv")
	gitInDir(t, dir, "commit", "-m", "merge branch-b")

	gitMergeConflict(t, dir, "branch-c")
	mergedABC := header +
		"1,A parent,open,P2,task,,,,,,,,{}\n" +
		"2,A child,open,P2,task,1,,,,,,,{}\n" +
		"1,B parent,open,P2,task,,,,,,,,{}\n" +
		"2,B child,open,P2,task,1,,,,,,,{}\n" +
		"1,C parent,open,P2,task,,,,,,,,{}\n" +
		"2,C child,open,P2,task,1,,,,,,,{}\n"
	os.WriteFile(filepath.Join(dir, "TASKS.csv"), []byte(mergedABC), 0644)
	gitInDir(t, dir, "add", "TASKS.csv")
	gitInDir(t, dir, "commit", "-m", "merge branch-c")

	// Run doctor.
	s := meads.NewFileStore(filepath.Join(dir, "TASKS.csv"))
	fixes, err := s.Doctor()
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	// 4 duplicates: B's 1, B's 2, C's 1, C's 2.
	if len(fixes) != 4 {
		t.Fatalf("expected 4 fixes, got %d: %+v", len(fixes), fixes)
	}

	tasks, err := s.Get(nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(tasks) != 6 {
		t.Fatalf("expected 6 tasks, got %d", len(tasks))
	}

	// All IDs unique.
	ids := map[int]bool{}
	for _, task := range tasks {
		if ids[task.ID] {
			t.Errorf("duplicate ID %d after Doctor", task.ID)
		}
		ids[task.ID] = true
	}

	// Each child should depend on its own parent.
	byTitle := map[string]meads.Task{}
	for _, task := range tasks {
		byTitle[task.Title] = task
	}
	for _, group := range []string{"A", "B", "C"} {
		parent := byTitle[group+" parent"]
		child := byTitle[group+" child"]
		if len(child.DependsOn) != 1 || child.DependsOn[0] != parent.ID {
			t.Errorf("%s child DependsOn = %v, want [%d]", group, child.DependsOn, parent.ID)
		}
	}
}

// TestDoctor_MergePreservesBaseDep tests that a task depending on
// a non-duplicated base task is left unchanged after doctor.
func TestDoctor_MergePreservesBaseDep(t *testing.T) {
	dir := t.TempDir()
	header := meads.InitCSV()

	gitInDir(t, dir, "init", "-b", "main")
	base := header + "1,Shared base,open,P1,task,,,,,,,,{}\n"
	os.WriteFile(filepath.Join(dir, "TASKS.csv"), []byte(base), 0644)
	gitInDir(t, dir, "add", "TASKS.csv")
	gitInDir(t, dir, "commit", "-m", "initial")

	// Branch B: adds task 2 depending on base task 1.
	gitInDir(t, dir, "checkout", "-b", "branch-b")
	bContent := header +
		"1,Shared base,open,P1,task,,,,,,,,{}\n" +
		"2,B work,open,P2,task,1,,,,,,,{}\n"
	os.WriteFile(filepath.Join(dir, "TASKS.csv"), []byte(bContent), 0644)
	gitInDir(t, dir, "add", "TASKS.csv")
	gitInDir(t, dir, "commit", "-m", "branch-b")

	// Branch C: also adds task 2 depending on base task 1.
	gitInDir(t, dir, "checkout", "main")
	gitInDir(t, dir, "checkout", "-b", "branch-c")
	cContent := header +
		"1,Shared base,open,P1,task,,,,,,,,{}\n" +
		"2,C work,open,P2,task,1,,,,,,,{}\n"
	os.WriteFile(filepath.Join(dir, "TASKS.csv"), []byte(cContent), 0644)
	gitInDir(t, dir, "add", "TASKS.csv")
	gitInDir(t, dir, "commit", "-m", "branch-c")

	// Merge.
	gitInDir(t, dir, "checkout", "main")
	gitInDir(t, dir, "merge", "branch-b")
	gitMergeConflict(t, dir, "branch-c")
	merged := header +
		"1,Shared base,open,P1,task,,,,,,,,{}\n" +
		"2,B work,open,P2,task,1,,,,,,,{}\n" +
		"2,C work,open,P2,task,1,,,,,,,{}\n"
	os.WriteFile(filepath.Join(dir, "TASKS.csv"), []byte(merged), 0644)
	gitInDir(t, dir, "add", "TASKS.csv")
	gitInDir(t, dir, "commit", "-m", "resolve merge")

	s := meads.NewFileStore(filepath.Join(dir, "TASKS.csv"))
	fixes, err := s.Doctor()
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if len(fixes) != 1 {
		t.Fatalf("expected 1 fix, got %d", len(fixes))
	}

	tasks, err := s.Get(nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}

	byTitle := map[string]meads.Task{}
	for _, task := range tasks {
		byTitle[task.Title] = task
	}

	// B work keeps ID 2, still depends on base task 1.
	bWork := byTitle["B work"]
	if bWork.ID != 2 {
		t.Errorf("B work ID = %d, want 2", bWork.ID)
	}
	if len(bWork.DependsOn) != 1 || bWork.DependsOn[0] != 1 {
		t.Errorf("B work DependsOn = %v, want [1]", bWork.DependsOn)
	}

	// C work was renumbered but still depends on base task 1 (not remapped,
	// since task 1 was not a duplicate).
	cWork := byTitle["C work"]
	if cWork.ID == 2 {
		t.Error("C work should have been renumbered from 2")
	}
	if len(cWork.DependsOn) != 1 || cWork.DependsOn[0] != 1 {
		t.Errorf("C work DependsOn = %v, want [1] (base task)", cWork.DependsOn)
	}
}
