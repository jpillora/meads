package meads

import (
	"path/filepath"
	"strings"

	"github.com/go-git/go-billy/v5"
	"github.com/go-git/go-billy/v5/osfs"
)

// Store manages task storage backed by a billy.Filesystem.
type Store struct {
	fs   billy.Filesystem
	file string // e.g. "TASKS.md" or "TASKS.csv"
	fmt  Format
}

// NewStore creates a Store using the given filesystem and file path.
func NewStore(fs billy.Filesystem, file string) *Store {
	return &Store{fs: fs, file: file, fmt: detectFormat(file)}
}

// NewFileStore creates a Store backed by the OS filesystem.
// The file path is split into a directory (for osfs) and a basename.
func NewFileStore(file string) *Store {
	dir := filepath.Dir(file)
	base := filepath.Base(file)
	return &Store{
		fs:   osfs.New(dir),
		file: base,
		fmt:  detectFormat(base),
	}
}

// detectFormat returns the appropriate Format implementation based on file extension.
func detectFormat(file string) Format {
	if strings.HasSuffix(file, ".csv") {
		return csvFormat{}
	}
	return markdownFormat{}
}

// FS returns the underlying filesystem.
func (s *Store) FS() billy.Filesystem {
	return s.fs
}

// Path returns the file path within the filesystem.
func (s *Store) Path() string {
	return s.file
}
