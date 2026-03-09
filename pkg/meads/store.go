package meads

import (
	"path/filepath"
	"strings"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/osfs"
)

// Store manages task storage backed by a billy.Filesystem.
type Store struct {
	fs       billy.Filesystem
	file     string // e.g. "TASKS.md" or "TASKS.csv"
	csvMode  bool
	parseFn  func(string) File
	formatFn func(File) string
}

// NewStore creates a Store using the given filesystem and file path.
func NewStore(fs billy.Filesystem, file string) *Store {
	s := &Store{fs: fs, file: file}
	s.detectFormat()
	return s
}

// NewFileStore creates a Store backed by the OS filesystem.
// The file path is split into a directory (for osfs) and a basename.
func NewFileStore(file string) *Store {
	dir := filepath.Dir(file)
	s := &Store{
		fs:   osfs.New(dir),
		file: filepath.Base(file),
	}
	s.detectFormat()
	return s
}

// detectFormat sets parseFn, formatFn, and csvMode based on the file extension.
func (s *Store) detectFormat() {
	if strings.HasSuffix(s.file, ".csv") {
		s.csvMode = true
		s.parseFn = ParseCSV
		s.formatFn = FormatCSV
	} else {
		s.csvMode = false
		s.parseFn = ParseFile
		s.formatFn = FormatFile
	}
}

// FS returns the underlying filesystem.
func (s *Store) FS() billy.Filesystem {
	return s.fs
}

// Path returns the file path within the filesystem.
func (s *Store) Path() string {
	return s.file
}
