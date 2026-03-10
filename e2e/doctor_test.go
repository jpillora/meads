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
