package e2e

import (
	"testing"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/jpillora/meads/pkg/meads"
)

func TestStoreAccessors(t *testing.T) {
	fs := memfs.New()
	s := meads.NewStore(fs, "TASKS.csv")
	if s.FS() != fs {
		t.Error("FS() did not return expected filesystem")
	}
	if s.Path() != "TASKS.csv" {
		t.Errorf("Path() = %q", s.Path())
	}
}

func TestNewFileStore(t *testing.T) {
	s := meads.NewFileStore("/tmp/test-meads/TASKS.csv")
	if s.Path() != "TASKS.csv" {
		t.Errorf("Path() = %q", s.Path())
	}
	if s.FS() == nil {
		t.Error("FS() returned nil")
	}
}
