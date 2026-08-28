package meads

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// Tests for GitStore's config path (git mode phase 4, TASKS #61): the
// repo-global Config stored at ConfigRef, and the oid-keyed cache in front
// of it. Like gitstore_test.go and gitmutate_test.go, these run against real
// temporary git repositories via ExecGit rather than a fake, since what's
// under test - a real git ref's oid actually changing (or not) across
// writes, and the cache tracking that correctly under real concurrency - is
// precisely what a fake would rubber-stamp without exercising.

// --- helpers ---

// seedConfig JSON-marshals cfg and commits it directly onto ConfigRef via
// RefStore, bypassing GitStore.SetConfig entirely - the config analogue of
// seedTask in gitstore_test.go. Used both to seed a starting value and, in
// the invalidation test, to prove GitStore picks up a change it didn't make
// itself.
func seedConfig(t *testing.T, rs *RefStore, cfg Config) OID {
	t.Helper()
	data, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshaling config: %v", err)
	}
	oid, err := rs.CommitFile(ConfigRef, ConfigFileName, data, ZeroOID, "meads test: seed config")
	if err != nil {
		t.Fatalf("seeding config: %v", err)
	}
	return oid
}

// countingGit wraps a Git implementation and counts calls to Output (used by
// RefStore.ResolveRef, i.e. a ref lookup) and OutputRaw (used by
// RefStore.ReadFileAtRef/ReadBlob to read a blob's content) separately, so a
// test can prove a specific claim - "resolved the ref but did not read the
// blob" - about which underlying git commands ran, rather than assuming it.
// Run and OutputWithInput are left promoted straight from the embedded Git,
// uncounted: nothing under test here needs them.
type countingGit struct {
	Git
	mu        sync.Mutex
	output    int
	outputRaw int
}

func (c *countingGit) Output(args ...string) (string, error) {
	c.mu.Lock()
	c.output++
	c.mu.Unlock()
	return c.Git.Output(args...)
}

func (c *countingGit) OutputRaw(args ...string) ([]byte, error) {
	c.mu.Lock()
	c.outputRaw++
	c.mu.Unlock()
	return c.Git.OutputRaw(args...)
}

func (c *countingGit) counts() (output, outputRaw int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.output, c.outputRaw
}

// --- 1. Absent config ref -> DefaultConfig(), no error ---

func TestGitConfig_Absent_ReturnsDefaults(t *testing.T) {
	gs, _, _ := newGitStoreRepo(t)

	got, err := gs.Config()
	if err != nil {
		t.Fatalf("Config() on a repo with no config ref: %v", err)
	}
	if want := DefaultConfig(); got != want {
		t.Errorf("Config() = %+v, want defaults %+v", got, want)
	}
}

// --- 2. Round trip: SetConfig then Config returns what was written ---

func TestGitConfig_SetConfig_RoundTrip(t *testing.T) {
	gs, _, _ := newGitStoreRepo(t)

	want := Config{RemoteLocking: true, PushInterval: "5m"}
	if err := gs.SetConfig(want); err != nil {
		t.Fatalf("SetConfig(%+v): %v", want, err)
	}

	got, err := gs.Config()
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if got != want {
		t.Errorf("Config() after SetConfig(%+v) = %+v, want it back unchanged", want, got)
	}
}

// --- 3. Partial config: an unset field falls back to its default ---

func TestGitConfig_PartialConfig_UnsetFieldFallsBackToDefault(t *testing.T) {
	gs, _, _ := newGitStoreRepo(t)

	if err := gs.SetConfig(Config{RemoteLocking: true}); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	got, err := gs.Config()
	if err != nil {
		t.Fatalf("Config: %v", err)
	}
	if !got.RemoteLocking {
		t.Errorf("RemoteLocking = false, want true (the field that was set)")
	}
	if want := DefaultConfig().PushInterval; got.PushInterval != want {
		t.Errorf("PushInterval = %q, want default %q (this field was never set)", got.PushInterval, want)
	}
}

// --- 4. PushIntervalDuration: valid, empty, and malformed input ---

func TestConfig_PushIntervalDuration(t *testing.T) {
	defaultDuration := DefaultConfig().PushIntervalDuration()
	if defaultDuration != time.Minute {
		t.Fatalf("DefaultConfig().PushIntervalDuration() = %v, want 1m (sanity check before using it as 'the default' below)", defaultDuration)
	}

	tests := []struct {
		name string
		in   string
		want time.Duration
	}{
		{"valid duration", "30s", 30 * time.Second},
		{"valid longer duration", "2h", 2 * time.Hour},
		{"empty falls back to default", "", defaultDuration},
		{"malformed falls back to default", "not-a-duration", defaultDuration},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := Config{PushInterval: tt.in}
			if got := cfg.PushIntervalDuration(); got != tt.want {
				t.Errorf("Config{PushInterval: %q}.PushIntervalDuration() = %v, want %v", tt.in, got, tt.want)
			}
		})
	}
}

// --- 5. Cache hit: a second Config() call does not re-read the blob ---

func TestGitConfig_CacheHit_SkipsBlobRead(t *testing.T) {
	_, rs, dir := newGitStoreRepo(t)
	seedConfig(t, rs, Config{RemoteLocking: true, PushInterval: "7m"})

	// A fresh GitStore wrapping a counting Git, pointed at the same repo, so
	// its cache starts empty and its first Config() call is a genuine miss.
	cg := &countingGit{Git: &ExecGit{Dir: dir}}
	gs := NewGitStore(cg)

	first, err := gs.Config()
	if err != nil {
		t.Fatalf("first Config(): %v", err)
	}
	_, outputRawAfterFirst := cg.counts()
	if outputRawAfterFirst == 0 {
		t.Fatal("first Config() never called OutputRaw - this test is not exercising a real blob read, so it can't prove the second call skips one")
	}

	second, err := gs.Config()
	if err != nil {
		t.Fatalf("second Config(): %v", err)
	}
	_, outputRawAfterSecond := cg.counts()
	if outputRawAfterSecond != outputRawAfterFirst {
		t.Errorf("second Config() call re-read the blob: OutputRaw count %d -> %d, want unchanged (an unchanged ref must be served from cache)",
			outputRawAfterFirst, outputRawAfterSecond)
	}
	if second != first {
		t.Errorf("second Config() = %+v, want it to match the first %+v", second, first)
	}
}

// --- 6. Cache invalidation: an external change to the ref is picked up ---

func TestGitConfig_CacheInvalidation_ExternalChangeIsPickedUp(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)
	seedConfig(t, rs, Config{RemoteLocking: false, PushInterval: "1m"})

	first, err := gs.Config()
	if err != nil {
		t.Fatalf("first Config(): %v", err)
	}
	if first.RemoteLocking {
		t.Fatalf("first Config() = %+v, want RemoteLocking=false", first)
	}

	// Bypass SetConfig entirely: write directly via RefStore, exactly the
	// "another process changed it" scenario the cache must not hide.
	oldOID, err := rs.ResolveRef(ConfigRef)
	if err != nil {
		t.Fatalf("ResolveRef before external write: %v", err)
	}
	data, err := json.Marshal(Config{RemoteLocking: true, PushInterval: "9m"})
	if err != nil {
		t.Fatalf("marshaling: %v", err)
	}
	if _, err := rs.CommitFile(ConfigRef, ConfigFileName, data, oldOID, "external write"); err != nil {
		t.Fatalf("external CommitFile: %v", err)
	}

	second, err := gs.Config()
	if err != nil {
		t.Fatalf("second Config(): %v", err)
	}
	want := Config{RemoteLocking: true, PushInterval: "9m"}
	if second != want {
		t.Errorf("Config() after an external write = %+v, want %+v (the cache must not hide a change it didn't make)", second, want)
	}
}

// --- 7. Absent-then-created: the new value must be seen ---
//
// This is the trap in requirement 2: a cache that remembers "the ref was
// absent" as a bare bool, and never checks again, would keep returning
// defaults forever even after a config ref shows up.

func TestGitConfig_AbsentThenCreated_NewValueIsSeen(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)

	first, err := gs.Config()
	if err != nil {
		t.Fatalf("first Config() on an absent ref: %v", err)
	}
	if want := DefaultConfig(); first != want {
		t.Fatalf("first Config() = %+v, want defaults %+v", first, want)
	}

	seedConfig(t, rs, Config{RemoteLocking: true, PushInterval: "42s"})

	second, err := gs.Config()
	if err != nil {
		t.Fatalf("second Config() after the ref was created: %v", err)
	}
	want := Config{RemoteLocking: true, PushInterval: "42s"}
	if second != want {
		t.Errorf("Config() after the config ref appeared = %+v, want %+v (an absent-ref cache must not be trusted forever)", second, want)
	}
}

// --- 8. SetConfig concurrency: no corruption, race-clean ---

func TestGitConfig_SetConfig_ConcurrentWritersNoCorruption(t *testing.T) {
	gs, _, _ := newGitStoreRepo(t)

	cfgA := Config{RemoteLocking: true, PushInterval: "2m"}
	cfgB := Config{RemoteLocking: false, PushInterval: "3m"}

	start := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(2)
	var errA, errB error
	go func() {
		defer wg.Done()
		<-start
		errA = gs.SetConfig(cfgA)
	}()
	go func() {
		defer wg.Done()
		<-start
		errB = gs.SetConfig(cfgB)
	}()
	close(start)
	wg.Wait()

	if errA != nil && errB != nil {
		t.Fatalf("both concurrent SetConfig calls failed cleanly: errA=%v errB=%v - with only two contenders and %d retries available, at least one must succeed", errA, errB, maxCASRetries)
	}

	// Re-read from git rather than trusting either goroutine's return value,
	// and via a brand new GitStore so the assertion can't be satisfied by a
	// stale in-memory cache either.
	fresh := NewGitStore(gs.git)
	got, err := fresh.Config()
	if err != nil {
		t.Fatalf("Config() after concurrent SetConfig: %v", err)
	}
	if got != cfgA && got != cfgB {
		t.Fatalf("final config = %+v, want exactly one of the two written values %+v or %+v (never a merge/corruption of both)", got, cfgA, cfgB)
	}
}

// --- 9. Forward compatibility: an unknown field survives read/modify/write ---

func TestGitConfig_SetConfig_PreservesUnknownFields(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)

	// Write config.json with a field this version of Config doesn't know
	// about, simulating a newer meads binary having written it first.
	raw := []byte(`{"remote_locking":false,"future_field":"future_value"}`)
	if _, err := rs.CommitFile(ConfigRef, ConfigFileName, raw, ZeroOID, "meads test: seed config with unknown field"); err != nil {
		t.Fatalf("seeding raw config: %v", err)
	}

	if err := gs.SetConfig(Config{RemoteLocking: true, PushInterval: "6m"}); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}

	content, _, err := rs.ReadFileAtRef(ConfigRef, ConfigFileName)
	if err != nil {
		t.Fatalf("ReadFileAtRef after SetConfig: %v", err)
	}
	var stored map[string]any
	if err := json.Unmarshal(content, &stored); err != nil {
		t.Fatalf("parsing stored config.json: %v", err)
	}
	if stored["future_field"] != "future_value" {
		t.Errorf("config.json after SetConfig = %v, want future_field=future_value preserved (forward compatibility)", stored)
	}
	if stored["remote_locking"] != true {
		t.Errorf("config.json after SetConfig = %v, want remote_locking=true (the field SetConfig changed)", stored)
	}
	if stored["push_interval"] != "6m" {
		t.Errorf("config.json after SetConfig = %v, want push_interval=6m", stored)
	}
	if stored[gitRefProtocolVersionKey] != float64(GitRefProtocolVersion) {
		t.Errorf("config.json after SetConfig = %v, want %s=%d", stored, gitRefProtocolVersionKey, GitRefProtocolVersion)
	}

	// An unknown field must not break a normal Config() read either.
	cfg, err := gs.Config()
	if err != nil {
		t.Fatalf("Config() with an unknown field present: %v", err)
	}
	if want := (Config{RemoteLocking: true, PushInterval: "6m"}); cfg != want {
		t.Errorf("Config() = %+v, want %+v", cfg, want)
	}
}

func TestGitConfig_LegacyConfigIsAcceptedAndNextMutationWritesProtocolVersion(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)
	// This is exactly the config shape written before the protocol marker was
	// introduced. It is the v1 protocol, not an unknown protocol.
	if _, err := rs.CommitFile(ConfigRef, ConfigFileName, []byte(`{"push_interval":"7m"}`), ZeroOID, "legacy config"); err != nil {
		t.Fatalf("seeding legacy config: %v", err)
	}
	if _, err := gs.LoadAll(); err != nil {
		t.Fatalf("LoadAll with legacy config: %v", err)
	}
	if _, err := gs.Create(Task{Title: "stamps protocol"}); err != nil {
		t.Fatalf("Create with legacy config: %v", err)
	}
	content, _, err := rs.ReadFileAtRef(ConfigRef, ConfigFileName)
	if err != nil {
		t.Fatalf("reading stamped config: %v", err)
	}
	var stored map[string]any
	if err := json.Unmarshal(content, &stored); err != nil {
		t.Fatalf("parsing stamped config: %v", err)
	}
	if got := stored[gitRefProtocolVersionKey]; got != float64(GitRefProtocolVersion) {
		t.Errorf("%s = %v, want %d", gitRefProtocolVersionKey, got, GitRefProtocolVersion)
	}
	if got := stored["push_interval"]; got != "7m" {
		t.Errorf("push_interval = %v, want legacy setting preserved", got)
	}
}

func TestGitConfig_NewerProtocolRequiresMdUpgradeBeforeReadOrWrite(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)
	future := GitRefProtocolVersion + 1
	raw := []byte(fmt.Sprintf(`{"git_ref_protocol_version":%d}`, future))
	if _, err := rs.CommitFile(ConfigRef, ConfigFileName, raw, ZeroOID, "future protocol"); err != nil {
		t.Fatalf("seeding future config: %v", err)
	}

	_, err := gs.LoadAll()
	if !errors.Is(err, ErrGitRefProtocolUpgradeRequired) {
		t.Fatalf("LoadAll error = %v, want ErrGitRefProtocolUpgradeRequired", err)
	}
	if !strings.Contains(err.Error(), "upgrade md") || !strings.Contains(err.Error(), strconv.Itoa(future)) {
		t.Errorf("LoadAll error = %q, want upgrade instruction and protocol version", err)
	}
	if _, err := gs.Create(Task{Title: "must not be written"}); !errors.Is(err, ErrGitRefProtocolUpgradeRequired) {
		t.Fatalf("Create error = %v, want ErrGitRefProtocolUpgradeRequired", err)
	}
	refs, err := rs.ListRefs(TasksRefPrefix)
	if err != nil {
		t.Fatal(err)
	}
	if len(refs) != 0 {
		t.Errorf("Create under future protocol wrote task refs: %v", refs)
	}
}

func TestGitConfig_NewerFetchedProtocolStopsIntegration(t *testing.T) {
	gs, rs, _ := newGitStoreRepo(t)
	if err := gs.SetConfig(DefaultConfig()); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	future := GitRefProtocolVersion + 1
	raw := []byte(fmt.Sprintf(`{"git_ref_protocol_version":%d}`, future))
	remoteConfigRef := RemoteRefNamespace + "config"
	if _, err := rs.CommitFile(remoteConfigRef, ConfigFileName, raw, ZeroOID, "future remote protocol"); err != nil {
		t.Fatalf("seeding future remote config: %v", err)
	}
	if _, err := gs.Integrate(); !errors.Is(err, ErrGitRefProtocolUpgradeRequired) {
		t.Fatalf("Integrate error = %v, want ErrGitRefProtocolUpgradeRequired", err)
	}
}
