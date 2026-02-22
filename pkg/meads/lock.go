package meads

import (
	"crypto/rand"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/go-git/go-billy/v5/util"
)

// acquireLock appends a lock line to the file and checks if we won the race.
// Returns the lock ID and file contents (without any lock lines) on success.
func (s *Store) acquireLock() (string, string, error) {
	id, err := randomID()
	if err != nil {
		return "", "", fmt.Errorf("generating lock id: %w", err)
	}
	now := time.Now().Unix()
	lockLine := fmt.Sprintf("\nlock:%s:%d\n", id, now)
	f, err := s.fs.OpenFile(s.file, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return "", "", fmt.Errorf("opening %s for lock: %w", s.file, err)
	}
	_, err = f.Write([]byte(lockLine))
	f.Close()
	if err != nil {
		return "", "", fmt.Errorf("writing lock: %w", err)
	}
	// Read back and check if we hold the lock.
	data, err := util.ReadFile(s.fs, s.file)
	if err != nil {
		return "", "", fmt.Errorf("reading %s after lock: %w", s.file, err)
	}
	content := string(data)
	// Find the first non-expired lock: line.
	for _, line := range strings.Split(content, "\n") {
		if !strings.HasPrefix(line, "lock:") {
			continue
		}
		// Parse timestamp from lock:<id>:<timestamp>
		parts := strings.SplitN(line, ":", 3)
		if len(parts) != 3 {
			continue // malformed lock line, skip
		}
		ts, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil {
			continue // malformed timestamp, skip
		}
		// Expired locks (>60s) are ignored
		if now-ts > 60 {
			continue
		}
		if parts[1] == id {
			// We won. Strip all lock lines and return clean content.
			clean := stripLockLines(content)
			return id, clean, nil
		}
		// Someone else won.
		return "", "", fmt.Errorf("lock contention: another writer holds the lock")
	}
	return "", "", fmt.Errorf("lock line not found after write")
}

// releaseLock writes the final content back to the file, removing all lock lines.
func (s *Store) releaseLock(content string) error {
	clean := stripLockLines(content)
	return util.WriteFile(s.fs, s.file, []byte(clean), 0644)
}

func stripLockLines(s string) string {
	lines := strings.Split(s, "\n")
	var out []string
	for _, line := range lines {
		if !strings.HasPrefix(line, "lock:") {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

func randomID() (string, error) {
	b := make([]byte, 16)
	_, err := rand.Read(b)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%x", b), nil
}
