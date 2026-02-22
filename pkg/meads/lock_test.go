package meads

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-git/go-billy/v5/memfs"
	"github.com/go-git/go-billy/v5/util"
)

func newTestStore(t *testing.T, content string) *Store {
	t.Helper()
	fs := memfs.New()
	if content != "" {
		if err := util.WriteFile(fs, "TASKS.md", []byte(content), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return NewStore(fs, "TASKS.md")
}

func TestAcquireLock_Basic(t *testing.T) {
	s := newTestStore(t, "# Tasks\n")
	id, content, err := s.acquireLock()
	if err != nil {
		t.Fatalf("acquireLock failed: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty lock id")
	}
	if !strings.HasPrefix(content, "# Tasks\n") {
		t.Errorf("content = %q, want prefix %q", content, "# Tasks\n")
	}
	if strings.Contains(content, "lock:") {
		t.Errorf("content should not contain lock lines: %q", content)
	}
}

func TestReleaseLock_StripsLockLines(t *testing.T) {
	s := newTestStore(t, "")
	original := "# Tasks\n\n## 1 Do stuff\n"
	withLocks := original + "\nlock:abc123:1234567890\nlock:def456:1234567890\n"
	if err := s.releaseLock(withLocks); err != nil {
		t.Fatalf("releaseLock failed: %v", err)
	}
	data, err := util.ReadFile(s.fs, s.file)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if strings.Contains(got, "lock:") {
		t.Errorf("lock lines remain in file: %q", got)
	}
}

func TestStripLockLines(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"no locks", "hello\nworld", "hello\nworld"},
		{"one lock", "hello\nlock:abc:123\nworld", "hello\nworld"},
		{"multiple locks", "a\nlock:x:1\nb\nlock:y:2\nc", "a\nb\nc"},
		{"only locks", "lock:a:1\nlock:b:2", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := stripLockLines(tt.input)
			if got != tt.want {
				t.Errorf("stripLockLines(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestAcquireLock_ExpiredLockIgnored(t *testing.T) {
	expired := fmt.Sprintf("lock:oldwriter:%d", time.Now().Unix()-120)
	s := newTestStore(t, "# Tasks\n"+expired+"\n")

	id, content, err := s.acquireLock()
	if err != nil {
		t.Fatalf("should succeed past expired lock, got: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty lock id")
	}
	if strings.Contains(content, "lock:") {
		t.Errorf("content should not contain lock lines: %q", content)
	}
}

func TestAcquireLock_ActiveLockBlocks(t *testing.T) {
	active := fmt.Sprintf("lock:otherwriter:%d", time.Now().Unix())
	s := newTestStore(t, "# Tasks\n"+active+"\n")

	_, _, err := s.acquireLock()
	if err == nil {
		t.Fatal("expected lock contention error")
	}
	if !strings.Contains(err.Error(), "lock contention") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestAcquireLock_MalformedLockIgnored(t *testing.T) {
	s := newTestStore(t, "# Tasks\nlock:nope\nlock:also:bad:extra\n")

	id, _, err := s.acquireLock()
	if err != nil {
		t.Fatalf("should succeed past malformed locks, got: %v", err)
	}
	if id == "" {
		t.Fatal("expected non-empty lock id")
	}
}

func TestConcurrentWriters_OneWins(t *testing.T) {
	s := newTestStore(t, "# Tasks\n")

	n := 10
	var mu sync.Mutex
	var winners []string
	var losers int
	var wg sync.WaitGroup
	wg.Add(n)

	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			id, _, err := s.acquireLock()
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				losers++
			} else {
				winners = append(winners, id)
			}
		}()
	}
	wg.Wait()

	if len(winners) != 1 {
		t.Errorf("expected exactly 1 winner, got %d", len(winners))
	}
	if losers != n-1 {
		t.Errorf("expected %d losers, got %d", n-1, losers)
	}
}

func TestAcquireRelease_RoundTrip(t *testing.T) {
	original := "# Tasks\n\n## 1 My task\n\n* status: open\n"
	s := newTestStore(t, original)

	id, content, err := s.acquireLock()
	if err != nil {
		t.Fatalf("acquire failed: %v", err)
	}
	if id == "" {
		t.Fatal("empty lock id")
	}

	updated := content + "\n## 2 New task\n\n* status: open\n"
	if err := s.releaseLock(updated); err != nil {
		t.Fatalf("release failed: %v", err)
	}

	data, err := util.ReadFile(s.fs, s.file)
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	if strings.Contains(got, "lock:") {
		t.Error("lock lines remain after release")
	}
	if !strings.Contains(got, "## 2 New task") {
		t.Error("new content missing after release")
	}
}
