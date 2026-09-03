package meads

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"
)

// remoteProbeTimeout bounds each remote round-trip the one-shot clone
// resolution makes - the ls-remote that asks origin whether it has
// refs/meads/*, and the fetch that adopts them if it does.
//
// Without it these are unbounded, and they sit on the critical path of the
// FIRST meads command in every repo that has an origin and no tasks file:
// an unreachable or black-holed host hangs for the OS's TCP connect
// timeout, commonly tens of seconds and unbounded in the worst case, so a
// plain `md list` could block indefinitely - and, because a failed ask
// deliberately writes no marker (see resolveCloneBackend), block again on
// every subsequent command. Ten seconds leaves real headroom over a normal
// remote round-trip while capping a wedged remote to something a user waiting
// on one command will tolerate.
//
// A var, not a const, purely so tests can shorten it and still assert the
// bound in reasonable wall-clock time. Production never reassigns it.
var remoteProbeTimeout = 10 * time.Second

// InitCheckRef is the marker ref recording "I have asked origin, and it has
// no refs/meads/*" - the cache that makes clone resolution one-shot (see
// resolveCloneBackend). It points directly at a blob, no commit or tree -
// the same shape refs/meads/lock already uses (gitlock.go), for the same
// reason: the value has no useful history. The blob content is the origin
// URL that was checked, so a repo whose remote is later repointed can be
// spotted.
//
// It lives OUTSIDE refs/meads/, and that is load-bearing, not cosmetic:
//
//   - Detect and OpenTasks probe the whole refs/meads/ namespace, so a
//     marker inside it would flip the repo into git mode - the exact bug
//     this marker exists to fix
//   - the push refspec refs/meads/*:refs/meads/* does not match it ("-" is
//     not "/"), so it can never be pushed
//   - likewise the fetch refspec +refs/meads/*:refs/meads-remote/* never
//     touches it
//
// (all three verified empirically: git's wildcard-free patterns match only
// at "/" boundaries). It is per-clone local state, like
// <git-common-dir>/meads/last-push.
const InitCheckRef = "refs/meads-init-check"

// probeInitState lists, in ONE for-each-ref process, every ref under
// refs/meads/ plus the InitCheckRef marker - the steady-state probe
// OpenTasks runs per call, so staying at one process means the fast paths
// (refs exist, or marker exists) cost exactly what detection alone used to.
// Any git failure (including "not a repository") reports empty/false, so
// callers fold to the file backends rather than erroring.
func probeInitState(git Git) (meadsRefs []string, initChecked bool) {
	out, err := git.Output("for-each-ref", "--format=%(refname)", RefNamespace, InitCheckRef)
	if err != nil {
		return nil, false
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if line == InitCheckRef {
			initChecked = true
		} else if strings.HasPrefix(line, RefNamespace) {
			meadsRefs = append(meadsRefs, line)
		}
	}
	return meadsRefs, initChecked
}

// resolveCloneBackend disambiguates "no tasks file AND no local
// refs/meads/*" - either a genuinely uninitialised repo or a fresh clone of
// a git-mode repo, which nothing offline can tell apart. Exactly one remote
// round-trip decides it, and the answer is cached (in the marker ref, or in
// the adopted refs themselves) so it never repeats:
//
//  1. no origin remote → nothing to ask; write the marker, file mode.
//  2. git ls-remote origin 'refs/meads/*' has refs → a clone of a git-mode
//     repo: ADOPT (see adoptOriginRefs) and report BackendGit. The marker
//     is NOT written - the local refs now existing is itself the terminal
//     state.
//  3. ls-remote has no refs → write the marker, file mode. Creating
//     TASKS.md from here on is safe.
//
// An ls-remote FAILURE (offline, auth, or remoteProbeTimeout expiring)
// writes nothing and reports file mode for this invocation only: the marker
// means "asked, and the answer was no", which a failed ask is not - so the
// next call retries, and the repo self-heals the moment it is reachable
// again. Marker writes are best-effort: a failure to write one must not fail
// the command, only cost a repeated ls-remote next time.
//
// Every remote round-trip here is bounded by remoteProbeTimeout - see there
// for why an unbounded one would be a hang on the critical path of the first
// command in every repo.
func resolveCloneBackend(git Git) Backend {
	ctx, cancel := context.WithTimeout(context.Background(), remoteProbeTimeout)
	defer cancel()

	url, err := outputContext(ctx, git, "remote", "get-url", "origin")
	if err != nil {
		if ctx.Err() != nil {
			return BackendMarkdown // could not even ask; retry next time (no marker)
		}
		writeInitCheck(git, "")
		return BackendMarkdown
	}
	out, err := outputContext(ctx, git, "ls-remote", "origin", RefNamespace+"*")
	if err != nil {
		return BackendMarkdown // unknown; retry next time (no marker)
	}
	if strings.TrimSpace(out) == "" {
		writeInitCheck(git, url)
		return BackendMarkdown
	}
	tasks, _, aerr := adoptOriginRefs(ctx, git, out)
	if aerr != nil {
		// The adopt failed somewhere after the ls-remote said yes. It may
		// have failed BEFORE the fetch (so the local namespace is still
		// empty, and retrying next time is right) or AFTER it (the fetch
		// landed and only EnsureFetchRefspec failed) - and in the latter
		// case reporting file mode would let the very next command write
		// TASKS.md into what is now demonstrably a git-mode repo, pinning
		// it to file mode forever (an existing tasks file short-circuits
		// this resolution). So ask the refs themselves rather than assume.
		if refs, _ := probeInitState(git); len(refs) > 0 {
			return BackendGit
		}
		return BackendMarkdown
	}
	noun := "refs"
	if tasks == 1 {
		noun = "ref"
	}
	fmt.Fprintf(os.Stderr, "meads: adopted %d task %s from origin\n", tasks, noun)
	return BackendGit
}

// adoptOriginRefs fetches origin's refs/meads/* straight into the local
// namespace and ensures the ordinary fetch refspec for later fetches. The
// direct fetch is safe by construction: it runs only when the local
// namespace is empty, so there is nothing to lose (unlike a fetch into a
// populated namespace, which is why the day-to-day fetch refspec lands in
// refs/meads-remote/* instead - see FetchRefspec). lsRemoteOut is the
// already-fetched `ls-remote origin refs/meads/*` output, reused so the
// adopt costs no second round-trip; the returned task count is parsed from
// it (refs under refs/meads/tasks/ with a numeric id). The fetch is bounded
// by ctx - see remoteProbeTimeout.
func adoptOriginRefs(ctx context.Context, git Git, lsRemoteOut string) (taskRefs int, outcome FetchRefspecOutcome, err error) {
	for _, line := range strings.Split(lsRemoteOut, "\n") {
		fields := strings.Fields(line) // "<oid>\t<refname>"
		if len(fields) == 2 {
			if _, ok := taskIDFromRef(TasksRefPrefix, fields[1]); ok {
				taskRefs++
			}
		}
	}
	if err := runContext(ctx, git, "fetch", "origin", "+"+RefNamespace+"*:"+RefNamespace+"*"); err != nil {
		return 0, FetchRefspecNoOrigin, fmt.Errorf("adopting %s* from origin: %w", RefNamespace, err)
	}
	outcome, err = EnsureFetchRefspec(git)
	if err != nil {
		return 0, FetchRefspecNoOrigin, err
	}
	return taskRefs, outcome, nil
}

// originMeadsRefs returns origin's advertised refs/meads/* as raw ls-remote
// output ("" when origin has none). An error means the ask itself failed
// (no origin remote, offline, auth, or ctx expiring) and says nothing about
// the refs.
func originMeadsRefs(ctx context.Context, git Git) (string, error) {
	if _, err := outputContext(ctx, git, "remote", "get-url", "origin"); err != nil {
		return "", fmt.Errorf("no origin remote: %w", err)
	}
	return outputContext(ctx, git, "ls-remote", "origin", RefNamespace+"*")
}

// writeInitCheck writes the marker ref: a blob holding the origin URL that
// was checked, referenced directly by InitCheckRef. Best-effort by
// contract (see resolveCloneBackend) - every error is swallowed, since the
// worst case is one repeated ls-remote on the next invocation.
func writeInitCheck(git Git, originURL string) {
	oid, err := git.OutputWithInput(originURL+"\n", "hash-object", "-w", "--stdin")
	if err != nil {
		return
	}
	_ = git.Run("update-ref", InitCheckRef, oid)
}
