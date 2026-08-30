package meads

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
)

func benchmarkBareRepo(b *testing.B) string {
	b.Helper()
	dir := b.TempDir()
	if err := (&ExecGit{Dir: dir}).Run("init", "--quiet", "--bare", "."); err != nil {
		b.Fatalf("git init --bare: %v", err)
	}
	return dir
}

func benchmarkGitBackends(b *testing.B, run func(*testing.B, Git)) {
	b.Helper()
	b.Run("native-git", func(b *testing.B) {
		run(b, &ExecGit{Dir: benchmarkBareRepo(b)})
	})
	b.Run("tigo", func(b *testing.B) {
		git := NewWazeroGit(benchmarkBareRepo(b))
		b.Cleanup(func() { _ = git.Close(context.Background()) })
		run(b, git)
	})
}

// BenchmarkGitBackendCommitFile compares one complete Meads ref mutation:
// blob, single-entry tree, commit, and compare-and-swap ref update.
func BenchmarkGitBackendCommitFile(b *testing.B) {
	benchmarkGitBackends(b, func(b *testing.B, git Git) {
		refs := NewRefStore(git)
		prev, err := refs.CommitFile(
			"refs/meads/tasks/1", TaskFileName, []byte(`{"id":1,"iteration":-1}`),
			ZeroOID, "benchmark seed",
		)
		if err != nil {
			b.Fatalf("seed CommitFile: %v", err)
		}
		b.ReportAllocs()
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			content := []byte(fmt.Sprintf(`{"id":1,"iteration":%d}`, i))
			prev, err = refs.CommitFile(
				"refs/meads/tasks/1", TaskFileName, content, prev, "benchmark update",
			)
			if err != nil {
				b.Fatalf("CommitFile %d: %v", i, err)
			}
		}
	})
}

func seedBenchmarkTasks(b *testing.B, dir string, count int) {
	b.Helper()
	refs := NewRefStore(&ExecGit{Dir: dir})
	for id := 1; id <= count; id++ {
		content, err := json.Marshal(Task{
			ID: id, Title: fmt.Sprintf("Task %d", id), Status: "open", Priority: "P2", Type: "task",
		})
		if err != nil {
			b.Fatal(err)
		}
		if _, err := refs.CommitFile(
			TasksRefPrefix+fmt.Sprint(id), TaskFileName, content, ZeroOID, "benchmark seed",
		); err != nil {
			b.Fatalf("seed task %d: %v", id, err)
		}
	}
}

// BenchmarkGitBackendLoadAll100 compares Meads' optimized two-command read:
// prefix-scan all refs, then cat-file batch all task.json blobs.
func BenchmarkGitBackendLoadAll100(b *testing.B) {
	dir := benchmarkBareRepo(b)
	seedBenchmarkTasks(b, dir, 100)
	for _, backend := range []struct {
		name string
		new  func() Git
	}{
		{name: "native-git", new: func() Git { return &ExecGit{Dir: dir} }},
		{name: "tigo", new: func() Git { return NewWazeroGit(dir) }},
	} {
		b.Run(backend.name, func(b *testing.B) {
			git := backend.new()
			if wasm, ok := git.(*WazeroGit); ok {
				b.Cleanup(func() { _ = wasm.Close(context.Background()) })
			}
			store := NewGitStore(git)
			if tasks, err := store.LoadAll(); err != nil || len(tasks) != 100 {
				b.Fatalf("warm LoadAll = %d tasks, %v", len(tasks), err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				tasks, err := store.LoadAll()
				if err != nil || len(tasks) != 100 {
					b.Fatalf("LoadAll = %d tasks, %v", len(tasks), err)
				}
			}
		})
	}
}

// BenchmarkGitBackendStartupAndList includes backend construction and the
// first command, approximating one short-lived `md list` process. Wazero uses
// its persistent compilation cache after the first run.
func BenchmarkGitBackendStartupAndList(b *testing.B) {
	dir := benchmarkBareRepo(b)
	seedBenchmarkTasks(b, dir, 1)
	b.Run("native-git", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			refs, err := NewRefStore(&ExecGit{Dir: dir}).ListRefs(TasksRefPrefix)
			if err != nil || len(refs) != 1 {
				b.Fatalf("ListRefs = %d refs, %v", len(refs), err)
			}
		}
	})
	b.Run("tigo", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			git := NewWazeroGit(dir)
			refs, err := NewRefStore(git).ListRefs(TasksRefPrefix)
			closeErr := git.Close(context.Background())
			if err != nil || closeErr != nil || len(refs) != 1 {
				b.Fatalf("ListRefs = %d refs, %v; close = %v", len(refs), err, closeErr)
			}
		}
	})
}
