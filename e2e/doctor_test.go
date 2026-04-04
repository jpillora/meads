package e2e

import (
	"testing"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-billy/v5/util"
	"github.com/jpillora/meads/pkg/meads"
)

func TestDoctor_NoDuplicates(t *testing.T) {
	fs := memfs.New()
	s := meads.NewStore(fs, "TASKS.csv")
	// Add two tasks normally — no duplicates.
	s.Add(meads.Task{Title: "Task one", Status: "open"})
	s.Add(meads.Task{Title: "Task two", Status: "open"})

	fixes, err := s.Doctor()
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if len(fixes) != 0 {
		t.Fatalf("expected no fixes, got %d", len(fixes))
	}
}

func TestDoctor_DuplicateIDs_CSV(t *testing.T) {
	fs := memfs.New()
	s := meads.NewStore(fs, "TASKS.csv")
	// Simulate a post-merge file with duplicate IDs by writing raw CSV.
	header := meads.InitCSV()
	raw := header +
		"1,Task A,open,P1,task,,,,,,," + "{}\n" +
		"2,Task B,open,P2,task,,,,,,," + "{}\n" +
		"2,Task C from branch,open,P2,task,,,,,,," + "{}\n"
	util.WriteFile(fs, "TASKS.csv", []byte(raw), 0644)

	fixes, err := s.Doctor()
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if len(fixes) != 1 {
		t.Fatalf("expected 1 fix, got %d", len(fixes))
	}
	if fixes[0].OldID != 2 {
		t.Errorf("fix OldID = %d, want 2", fixes[0].OldID)
	}
	if fixes[0].NewID != 3 {
		t.Errorf("fix NewID = %d, want 3", fixes[0].NewID)
	}

	// Verify the file was fixed: should now have 3 unique tasks.
	tasks, err := s.Get(nil)
	if err != nil {
		t.Fatalf("Get after doctor: %v", err)
	}
	if len(tasks) != 3 {
		t.Fatalf("expected 3 tasks after fix, got %d", len(tasks))
	}
	ids := map[int]bool{}
	for _, task := range tasks {
		if ids[task.ID] {
			t.Errorf("duplicate ID %d still exists after Doctor", task.ID)
		}
		ids[task.ID] = true
	}
	if !ids[1] || !ids[2] || !ids[3] {
		t.Errorf("expected IDs {1,2,3}, got %v", ids)
	}
}

func TestDoctor_DuplicateIDs_Markdown(t *testing.T) {
	fs := memfs.New()
	s := meads.NewStore(fs, "TASKS.md")
	// Write raw markdown with duplicate ID 5.
	raw := "# TASKS\n\n## 5. First task\n\n* status: open\n\n## 5. Second task\n\n* status: open\n\n## 6. Third task\n\n* status: open\n"
	util.WriteFile(fs, "TASKS.md", []byte(raw), 0644)

	fixes, err := s.Doctor()
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if len(fixes) != 1 {
		t.Fatalf("expected 1 fix, got %d", len(fixes))
	}
	if fixes[0].OldID != 5 {
		t.Errorf("fix OldID = %d, want 5", fixes[0].OldID)
	}
	if fixes[0].NewID != 7 {
		t.Errorf("fix NewID = %d, want 7", fixes[0].NewID)
	}

	tasks, err := s.Get(nil)
	if err != nil {
		t.Fatalf("Get after doctor: %v", err)
	}
	if len(tasks) != 3 {
		t.Fatalf("expected 3 tasks after fix, got %d", len(tasks))
	}
}

func TestDoctor_MultipleDuplicates(t *testing.T) {
	fs := memfs.New()
	s := meads.NewStore(fs, "TASKS.csv")
	header := meads.InitCSV()
	// Two pairs of duplicates: ID 1 appears twice, ID 3 appears three times.
	raw := header +
		"1,A,open,P1,task,,,,,,," + "{}\n" +
		"1,B,open,P1,task,,,,,,," + "{}\n" +
		"3,C,open,P2,task,,,,,,," + "{}\n" +
		"3,D,open,P2,task,,,,,,," + "{}\n" +
		"3,E,open,P2,task,,,,,,," + "{}\n"
	util.WriteFile(fs, "TASKS.csv", []byte(raw), 0644)

	fixes, err := s.Doctor()
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if len(fixes) != 3 {
		t.Fatalf("expected 3 fixes, got %d", len(fixes))
	}

	tasks, err := s.Get(nil)
	if err != nil {
		t.Fatalf("Get after doctor: %v", err)
	}
	if len(tasks) != 5 {
		t.Fatalf("expected 5 tasks after fix, got %d", len(tasks))
	}
	ids := map[int]bool{}
	for _, task := range tasks {
		if ids[task.ID] {
			t.Errorf("duplicate ID %d still exists after Doctor", task.ID)
		}
		ids[task.ID] = true
	}
}

func TestDoctor_DependsOnFixup(t *testing.T) {
	fs := memfs.New()
	s := meads.NewStore(fs, "TASKS.csv")
	header := meads.InitCSV()
	// Simulate a merge where two branches both created tasks 1 and 2,
	// with task 2 depending on task 1 in each branch.
	raw := header +
		"1,Branch A task,open,P2,task,,,,,,,," + "{}\n" +
		"2,Branch A dep,open,P2,task,1,,,,,,," + "{}\n" +
		"1,Branch B task,open,P2,task,,,,,,,," + "{}\n" +
		"2,Branch B dep,open,P2,task,1,,,,,,," + "{}\n"
	util.WriteFile(fs, "TASKS.csv", []byte(raw), 0644)

	fixes, err := s.Doctor()
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if len(fixes) != 2 {
		t.Fatalf("expected 2 fixes, got %d", len(fixes))
	}

	tasks, err := s.Get(nil)
	if err != nil {
		t.Fatalf("Get after doctor: %v", err)
	}
	if len(tasks) != 4 {
		t.Fatalf("expected 4 tasks after fix, got %d", len(tasks))
	}

	// Build a map of title -> task for verification.
	byTitle := map[string]meads.Task{}
	for _, task := range tasks {
		byTitle[task.Title] = task
	}
	// Branch A tasks keep their original IDs.
	aTask := byTitle["Branch A task"]
	aDep := byTitle["Branch A dep"]
	if aTask.ID != 1 {
		t.Errorf("Branch A task ID = %d, want 1", aTask.ID)
	}
	if aDep.ID != 2 {
		t.Errorf("Branch A dep ID = %d, want 2", aDep.ID)
	}
	if len(aDep.DependsOn) != 1 || aDep.DependsOn[0] != 1 {
		t.Errorf("Branch A dep DependsOn = %v, want [1]", aDep.DependsOn)
	}
	// Branch B tasks were renumbered, and depends-on was updated.
	bTask := byTitle["Branch B task"]
	bDep := byTitle["Branch B dep"]
	if bTask.ID == 1 {
		t.Errorf("Branch B task should have been renumbered from 1")
	}
	if bDep.ID == 2 {
		t.Errorf("Branch B dep should have been renumbered from 2")
	}
	// Branch B dep should now depend on Branch B task's new ID.
	if len(bDep.DependsOn) != 1 || bDep.DependsOn[0] != bTask.ID {
		t.Errorf("Branch B dep DependsOn = %v, want [%d]", bDep.DependsOn, bTask.ID)
	}
}

func TestDoctor_EmptyFile(t *testing.T) {
	fs := memfs.New()
	s := meads.NewStore(fs, "TASKS.csv")
	// Doctor on a non-existent file should create the file and find no issues.
	fixes, err := s.Doctor()
	if err != nil {
		t.Fatalf("Doctor: %v", err)
	}
	if len(fixes) != 0 {
		t.Fatalf("expected no fixes, got %d", len(fixes))
	}
}
