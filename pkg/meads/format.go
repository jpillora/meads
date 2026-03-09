package meads

// Format defines the interface for parsing and formatting task files.
// Implemented by markdownFormat (TASKS.md) and csvFormat (TASKS.csv).
type Format interface {
	// Parse reads file content and returns a File with tasks.
	Parse(content string) File
	// Format writes a File back to the file's string representation.
	Format(f File) string
	// HasPreamble reports whether the format supports project-level metadata
	// (e.g. created/updated timestamps in a preamble section).
	HasPreamble() bool
	// EmptyFile returns the initial content for a new empty file.
	EmptyFile() string
}
