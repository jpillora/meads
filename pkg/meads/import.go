package meads

import (
	"fmt"
	"os"

	"github.com/go-git/go-billy/v5/util"
)

// Importer defines the interface for importing tasks from external sources.
type Importer interface {
	Name() string
	Import() ([]Task, error)
}

var importers = map[string]Importer{}

// RegisterImporter adds an importer to the registry.
func RegisterImporter(imp Importer) {
	importers[imp.Name()] = imp
}

// GetImporter returns a registered importer by name.
func GetImporter(name string) (Importer, error) {
	imp, ok := importers[name]
	if !ok {
		return nil, fmt.Errorf("unknown import target: %s", name)
	}
	return imp, nil
}

// ImportResult contains the results of an import operation.
type ImportResult struct {
	Imported int
	Skipped  int
	IDs      []int
}

// RunImport imports tasks from the given importer, deduplicating by
// "<name>-id" meta key. New tasks are appended via AddMany.
func (s *Store) RunImport(imp Importer) (ImportResult, error) {
	tasks, err := imp.Import()
	if err != nil {
		return ImportResult{}, fmt.Errorf("importing from %s: %w", imp.Name(), err)
	}
	// Load existing tasks for dedup.
	metaKey := imp.Name() + "-id"
	existing := make(map[string]bool)
	if data, err := util.ReadFile(s.fs, s.file); err == nil {
		f := ParseFile(stripLockLines(string(data)))
		for _, t := range f.Tasks {
			if v, ok := t.Meta[metaKey]; ok {
				existing[v] = true
			}
		}
	} else if !os.IsNotExist(err) {
		return ImportResult{}, fmt.Errorf("reading %s: %w", s.file, err)
	}
	var toAdd []Task
	skipped := 0
	for _, t := range tasks {
		id := t.Meta[metaKey]
		if id != "" && existing[id] {
			skipped++
			continue
		}
		toAdd = append(toAdd, t)
	}
	if len(toAdd) == 0 {
		return ImportResult{Skipped: skipped}, nil
	}
	ids, err := s.AddMany(toAdd)
	if err != nil {
		return ImportResult{}, err
	}
	return ImportResult{
		Imported: len(ids),
		Skipped:  skipped,
		IDs:      ids,
	}, nil
}
