package meads

import (
	"testing"

	"github.com/go-git/go-billy/v5/memfs"
)

func TestDetectFormat_CSV(t *testing.T) {
	f := detectFormat("TASKS.csv")
	if _, ok := f.(csvFormat); !ok {
		t.Errorf("detectFormat(TASKS.csv) returned %T, want csvFormat", f)
	}
}

func TestDetectFormat_MD(t *testing.T) {
	f := detectFormat("TASKS.md")
	if _, ok := f.(markdownFormat); !ok {
		t.Errorf("detectFormat(TASKS.md) returned %T, want markdownFormat", f)
	}
}

func TestDetectFormat_Default(t *testing.T) {
	f := detectFormat("TASKS.txt")
	if _, ok := f.(markdownFormat); !ok {
		t.Errorf("detectFormat(TASKS.txt) returned %T, want markdownFormat", f)
	}
}

func TestStoreAccessors(t *testing.T) {
	fs := memfs.New()
	s := NewStore(fs, "TASKS.csv")

	if s.FS() != fs {
		t.Error("FS() did not return the expected filesystem")
	}
	if s.Path() != "TASKS.csv" {
		t.Errorf("Path() = %q, want %q", s.Path(), "TASKS.csv")
	}
}

func TestNewFileStore(t *testing.T) {
	s := NewFileStore("/tmp/test-meads/TASKS.csv")
	if s.Path() != "TASKS.csv" {
		t.Errorf("Path() = %q, want %q", s.Path(), "TASKS.csv")
	}
	if s.FS() == nil {
		t.Error("FS() returned nil")
	}
}
