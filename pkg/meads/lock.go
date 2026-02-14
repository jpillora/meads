package meads

import (
	"crypto/rand"
	"fmt"
	"os"
	"strings"
)

// acquireLock appends a lock line to the file and checks if we won the race.
// Returns the lock ID and file contents (without any lock lines) on success.
func acquireLock(file string) (string, string, error) {
	id, err := randomID()
	if err != nil {
		return "", "", fmt.Errorf("generating lock id: %w", err)
	}
	lockLine := "\nlock:" + id + "\n"
	f, err := os.OpenFile(file, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return "", "", fmt.Errorf("opening %s for lock: %w", file, err)
	}
	_, err = f.WriteString(lockLine)
	f.Close()
	if err != nil {
		return "", "", fmt.Errorf("writing lock: %w", err)
	}
	// Read back and check if we hold the lock.
	data, err := os.ReadFile(file)
	if err != nil {
		return "", "", fmt.Errorf("reading %s after lock: %w", file, err)
	}
	content := string(data)
	// Find the first lock: line.
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(line, "lock:") {
			if line == "lock:"+id {
				// We won. Strip all lock lines and return clean content.
				clean := stripLockLines(content)
				return id, clean, nil
			}
			// Someone else won — clean up our lock line.
			cleaned := strings.Replace(content, lockLine, "", 1)
			os.WriteFile(file, []byte(cleaned), 0644)
			return "", "", fmt.Errorf("lock contention: another writer holds the lock")
		}
	}
	return "", "", fmt.Errorf("lock line not found after write")
}

// releaseLock writes the final content back to the file, removing all lock lines.
func releaseLock(file, content string) error {
	clean := stripLockLines(content)
	return os.WriteFile(file, []byte(clean), 0644)
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
