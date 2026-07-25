package meads

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// PushStateDir is the subdirectory of the git COMMON dir (see ShouldPush's
// doc comment) meads uses for its own local, per-clone, never-pushed push
// state: the last-push timestamp this file governs (lastPushFile below),
// and, in cmd/md, the most recent push attempt's captured output. Exported
// so cmd/md's spawner (which writes the latter) and this file (which writes
// the former) can never disagree about where that shared directory lives.
const PushStateDir = "meads"

// lastPushFile is ShouldPush/MarkPushed's timestamp file, under
// PushStateDir, holding time.Now().UnixNano() as decimal text.
const lastPushFile = "last-push"

// ShouldPush reports whether interval has elapsed since the last recorded
// push attempt under commonDir (see MarkPushed) - or true if there is no
// record at all, which covers both "never attempted" and "the record is
// unreadable/corrupt". Both fail OPEN (a push is due) rather than
// permanently wedging auto-push on a damaged local cache file: the very
// next successful MarkPushed overwrites it with a fresh, valid value
// regardless.
//
// commonDir MUST be the git COMMON directory (`git rev-parse
// --git-common-dir`), never a per-worktree --git-dir: every linked worktree
// of one repository shares a single common dir, and therefore shares a
// single push cadence here too - correct, since they already share one
// refs/meads/* ref store (see this file's siblings gitstore.go/
// gitmutate.go). Push cadence is deliberately per-CLONE, never shared
// across separate clones the way refs/meads/* itself is: this file must
// never be committed, pushed, or turned into a ref of its own - a shared
// cadence would make every clone fight over one timer instead of each
// pushing on its own schedule (see task 63).
func (g *GitStore) ShouldPush(commonDir string, interval time.Duration) (bool, error) {
	last, ok, err := readLastPush(commonDir)
	if err != nil {
		return false, err
	}
	if !ok {
		return true, nil
	}
	return time.Since(last) >= interval, nil
}

// MarkPushed records now as the last push attempt under commonDir, creating
// its PushStateDir subdirectory if this is this clone's first-ever push
// attempt.
//
// Call this at the moment a push is TRIGGERED, not once it SUCCEEDS:
// marking first (rather than after the fact) means a burst of mutation
// commands issued while one push is still in flight all see ShouldPush =
// false, so they never pile up redundant concurrent pushes against the same
// remote (see cmd/md's autoPush, the only caller). The trade-off is that a
// failed push also waits a full interval before the next attempt, rather
// than retrying on the very next command; given pushInterval defaults to a
// minute (gitconfig.go) and failures are usually transient connectivity,
// that trade favours never accumulating redundant concurrent pushes over
// faster retries.
func (g *GitStore) MarkPushed(commonDir string) error {
	dir := filepath.Join(commonDir, PushStateDir)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("creating %s: %w", dir, err)
	}
	path := filepath.Join(dir, lastPushFile)
	content := strconv.FormatInt(time.Now().UnixNano(), 10)
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}
	return nil
}

// readLastPush reads and parses commonDir's last-push timestamp file. ok is
// false if the file is absent OR its content is unparsable - both mean "no
// usable record", deliberately folded into one outcome rather than
// distinguished, matching ShouldPush's fail-open doc comment.
func readLastPush(commonDir string) (t time.Time, ok bool, err error) {
	path := filepath.Join(commonDir, PushStateDir, lastPushFile)
	data, rerr := os.ReadFile(path)
	if rerr != nil {
		if os.IsNotExist(rerr) {
			return time.Time{}, false, nil
		}
		return time.Time{}, false, fmt.Errorf("reading %s: %w", path, rerr)
	}
	ns, perr := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if perr != nil {
		return time.Time{}, false, nil // corrupt content: fail open, see ShouldPush
	}
	return time.Unix(0, ns), true, nil
}
