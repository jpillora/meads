package meads

import (
	"context"
	"errors"
	"fmt"
	"sort"
)

// IntegrateReport describes what Integrate did, as data, so callers can
// report it (cmd/md's auto-push prints a one-line summary per kind) without
// the integration itself printing anything.
type IntegrateReport struct {
	// Imported lists ids that existed only on remote-tracking and were
	// adopted locally (the local ref points at the very same commit the
	// fetch landed, so the task's whole history comes with it). This is how
	// another clone's tasks - including its re-homed conflict copies and
	// its deletions' tombstones - arrive.
	Imported []int
	// FastForwarded lists ids whose local ref advanced to the fetched tip
	// because the local side had not moved since the common ancestor (the
	// remote side was strictly ahead). This is how another clone's edits
	// and deletions land on tasks this clone never touched.
	FastForwarded []int
	// ConfigUpdated reports the config ref was adopted or fast-forwarded
	// from remote-tracking (see integrateConfig; a diverged config also
	// takes the fetched version - settings have no per-clone identity worth
	// fighting over).
	ConfigUpdated bool
	// Fixes lists Doctor's repairs over the contended remainder: id
	// mismatches repaired in place, and contended ids (create/create
	// duplicates and edit/edit divergences) whose local version was
	// re-homed at a fresh id - see GitStore.Doctor.
	Fixes []DoctorFix
}

// empty reports whether nothing at all happened - used by callers to stay
// quiet in the common no-change case.
func (r *IntegrateReport) empty() bool {
	return r == nil || (len(r.Imported) == 0 && len(r.FastForwarded) == 0 && !r.ConfigUpdated && len(r.Fixes) == 0)
}

// Pull fetches origin (landing its refs/meads/* in refs/meads-remote/* via
// the configured fetch refspec - see FetchRefspec) and then integrates what
// arrived: the two halves of "pull" for the task namespace. With no origin
// remote configured it is a no-op (nothing to ask), mirroring how
// resolveCloneBackend treats the same case. A fetch failure is returned as
// an error and Integrate is NOT run: remote-tracking state is stale
// exactly when the fetch failed, and integrating against stale state could
// renumber against facts that are no longer true.
//
// The fetch is a plain `git fetch origin`, fetching every configured
// refspec (ordinary branches included): it is the fetch half of what a
// user means by "pull", and costs one round-trip either way.
func (g *GitStore) Pull() (*IntegrateReport, error) {
	return g.PullContext(context.Background())
}

// PullContext is Pull bounded by ctx. The FETCH is the half that talks to a
// remote and so the half that can hang unboundedly (an unreachable host
// costs the OS's TCP connect timeout otherwise - see ContextGit); it is
// killed the moment ctx is done. Integrate, which follows, is purely local
// git work and is not individually cancellable, so ctx is checked once more
// before it starts rather than threaded through it.
func (g *GitStore) PullContext(ctx context.Context) (*IntegrateReport, error) {
	if err := runContext(ctx, g.git, "remote", "get-url", "origin"); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		return &IntegrateReport{}, nil // no origin: nothing to pull
	}
	if err := runContext(ctx, g.git, "fetch", "origin"); err != nil {
		return nil, fmt.Errorf("fetching origin: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return g.Integrate()
}

// Integrate reconciles local refs/meads/* with the last fetch's
// remote-tracking refs/meads-remote/* - every case that does NOT need a
// renumber, then Doctor for the contended remainder:
//
//   - an id only on remote-tracking is ADOPTED: the local ref is created
//     pointing at the very same commit (Prev: ZeroOID), so the task arrives
//     with its whole history and zero object copying.
//   - an id on both sides where the local ref is an ancestor of the
//     remote-tracking one is FAST-FORWARDED to the fetched tip (the local
//     side never moved; the other clone's edits and deletions land here).
//   - an id on both sides where remote-tracking is an ancestor of local is
//     LEFT ALONE: local is strictly ahead and the next push shares it.
//   - the config ref gets the same treatment (adopt or fast-forward; a
//     diverged config takes the fetched version outright - settings have no
//     per-clone identity worth fighting over, unlike task content).
//   - whatever remains shared-and-unreconciled (create/create duplicates
//     and edit/edit divergences) is Doctor's contended case: the local
//     version is re-homed at a fresh id and the id itself takes the fetched
//     version, so the next push converges (see GitStore.Doctor).
//
// The adopt/fast-forward batch commits as ONE atomic RefStore.AtomicUpdate
// transaction, re-derived from a fresh read on every retry attempt bounded
// by maxCASRetries (the same retry-trap discipline as casUpdate); Doctor
// then runs its own batch as usual. refs/meads-remote/* itself is strictly
// read-only input - it is owned by `git fetch`, never written here.
func (g *GitStore) Integrate() (*IntegrateReport, error) {
	report, err := g.integrateRefs()
	if err != nil {
		return nil, err
	}

	// The contended remainder: renumber local versions onto fresh ids and
	// reset the contended refs to the fetched versions (see GitStore.Doctor).
	fixes, err := g.Doctor()
	if err != nil {
		return nil, err
	}
	report.Fixes = fixes
	return report, nil
}

// integrateRefs runs the adopt/fast-forward batch - Integrate's first half,
// split out so its retry loop can RETURN on success instead of breaking out
// to a shared post-loop error check. That shape matters: a `lastErr` set by
// a lost race on an early attempt must not outlive the attempt that then
// succeeds, or a perfectly good integration is reported as
// "exhausted N attempts" while its refs have in fact already moved - which
// would abort Sync before its push and swallow the re-homing notice the
// causing command is supposed to print. Every other CAS loop in this
// package (casUpdate, Create, Doctor) returns from inside the loop for the
// same reason.
func (g *GitStore) integrateRefs() (*IntegrateReport, error) {
	var lastErr error
	for attempt := 0; attempt < maxCASRetries; attempt++ {
		// Re-derive the plan from a fresh read on every attempt, into a
		// FRESH report: a lost race must neither replay a stale plan nor
		// accumulate duplicate report entries (see casUpdate's doc comment
		// on the retry trap).
		report := &IntegrateReport{}
		updates, err := g.planIntegration(report)
		if err != nil {
			return nil, err
		}
		if len(updates) == 0 {
			return report, nil
		}
		if err := g.refs.AtomicUpdate(updates); err != nil {
			if !errors.Is(err, ErrCASConflict) {
				return nil, err
			}
			lastErr = err // lost the race: loop and re-read
			continue
		}
		return report, nil
	}
	return nil, fmt.Errorf("integrate: exhausted %d attempts: %w", maxCASRetries, lastErr)
}

// planIntegration computes the adopt/fast-forward half of one Integrate
// attempt, recording what it plans into report. Like Doctor's
// planDoctorFixes it moves no ref itself: the batch is submitted by
// Integrate, and only the winning attempt's report is kept (see there).
func (g *GitStore) planIntegration(report *IntegrateReport) ([]RefUpdate, error) {
	local, localOIDs, err := g.loadAllWithOIDs(TasksRefPrefix)
	if err != nil {
		return nil, err
	}
	remote, remoteOIDs, err := g.loadAllWithOIDs(RemoteTasksRefPrefix)
	if err != nil {
		return nil, err
	}

	var updates []RefUpdate

	// Shared ids: fast-forward where local is strictly behind. Anything
	// else shared (local ahead, genuinely contended) is left for Doctor.
	var shared []int
	for id := range remote {
		if _, ok := local[id]; ok {
			shared = append(shared, id)
		}
	}
	sort.Ints(shared)
	for _, id := range shared {
		lOID, rOID := localOIDs[id], remoteOIDs[id]
		if lOID == rOID {
			continue
		}
		base, err := g.refs.MergeBase(lOID, rOID)
		if err != nil {
			if errors.Is(err, ErrUnrelatedHistories) {
				continue // a duplicate: Doctor's case, not a fast-forward
			}
			return nil, err
		}
		if base == lOID {
			// Local never moved since the common ancestor: take the fetched
			// tip (edits AND deletions both propagate this way).
			updates = append(updates, RefUpdate{Name: g.TaskRef(id), Next: rOID, Prev: lOID})
			report.FastForwarded = append(report.FastForwarded, id)
		}
		// base == rOID: local is strictly ahead, leave for the next push.
		// anything else: diverged, leave for Doctor.
	}

	// Remote-only ids: adopt, pointing the local ref at the very same
	// commit the fetch landed (full history, zero object copying).
	var remoteOnly []int
	for id := range remote {
		if _, ok := local[id]; !ok {
			remoteOnly = append(remoteOnly, id)
		}
	}
	sort.Ints(remoteOnly)
	for _, id := range remoteOnly {
		updates = append(updates, RefUpdate{Name: g.TaskRef(id), Next: remoteOIDs[id], Prev: ZeroOID})
		report.Imported = append(report.Imported, id)
	}

	// The config ref: adopt or fast-forward (a diverged config takes the
	// fetched version outright - see IntegrateReport.ConfigUpdated).
	configUpdates, err := g.planConfigIntegration(report)
	if err != nil {
		return nil, err
	}
	updates = append(updates, configUpdates...)

	return updates, nil
}

// planConfigIntegration plans the config ref's share of an Integrate
// attempt (the config lives outside TasksRefPrefix, so it is planned
// separately from the task refs above).
func (g *GitStore) planConfigIntegration(report *IntegrateReport) ([]RefUpdate, error) {
	remoteOID, err := g.refs.ResolveRef(RemoteRefNamespace + "config")
	if err != nil {
		if errors.Is(err, ErrRefNotFound) {
			return nil, nil // the remote has no config to adopt
		}
		return nil, err
	}
	localOID, err := g.refs.ResolveRef(ConfigRef)
	if err != nil {
		if errors.Is(err, ErrRefNotFound) {
			// No local config at all: adopt the fetched one.
			report.ConfigUpdated = true
			return []RefUpdate{{Name: ConfigRef, Next: remoteOID, Prev: ZeroOID}}, nil
		}
		return nil, err
	}
	if localOID == remoteOID {
		return nil, nil
	}
	base, err := g.refs.MergeBase(localOID, remoteOID)
	if err != nil && !errors.Is(err, ErrUnrelatedHistories) {
		return nil, err
	}
	// Fast-forwardable, or flat-out diverged/unrelated: the fetched config
	// wins either way (see IntegrateReport.ConfigUpdated). Only a strictly
	// local-ahead config is kept (base == remoteOID), to be shared by the
	// next push.
	if err == nil && base == remoteOID {
		return nil, nil
	}
	report.ConfigUpdated = true
	return []RefUpdate{{Name: ConfigRef, Next: remoteOID, Prev: localOID}}, nil
}
