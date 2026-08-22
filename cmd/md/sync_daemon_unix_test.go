//go:build unix

package main

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jpillora/meads/pkg/meads"
)

func TestFinishSyncCycleKeepsNewGeneration(t *testing.T) {
	pidPath := filepath.Join(t.TempDir(), "sync.pid")
	if err := os.WriteFile(pidPath, []byte("999999\n"), 0600); err != nil {
		t.Fatal(err)
	}
	old := syncRequest{RepoDir: "/repo", CommonDir: "/repo/.git", Generation: 1, Delay: "1ms"}
	if err := writeSyncRequest(pidPath, old); err != nil {
		t.Fatal(err)
	}
	path := syncRequestPath(pidPath, old.CommonDir)
	newer := old
	newer.Generation = 2
	if err := writeSyncRequest(pidPath, newer); err != nil {
		t.Fatal(err)
	}

	sigs := make(chan os.Signal, 1)
	pending, terminate, err := finishSyncCycle(pidPath, []string{path}, []syncRequest{old}, sigs)
	if err != nil {
		t.Fatal(err)
	}
	if !pending || terminate {
		t.Fatalf("finish = pending %v, terminate %v; want true, false", pending, terminate)
	}
	got, err := readSyncRequest(path)
	if err != nil {
		t.Fatalf("new generation was removed: %v", err)
	}
	if got.Generation != 2 {
		t.Fatalf("generation = %d, want 2", got.Generation)
	}
}

func TestFinishSyncCycleUSR2DuringSyncForcesAnotherTimer(t *testing.T) {
	pidPath := filepath.Join(t.TempDir(), "sync.pid")
	if err := os.WriteFile(pidPath, []byte("999999\n"), 0600); err != nil {
		t.Fatal(err)
	}
	req := syncRequest{RepoDir: "/repo", CommonDir: "/repo/.git", Generation: 1, Delay: "1ms"}
	if err := writeSyncRequest(pidPath, req); err != nil {
		t.Fatal(err)
	}
	path := syncRequestPath(pidPath, req.CommonDir)
	sigs := make(chan os.Signal, 1)
	sigs <- syscall.SIGUSR2
	pending, terminate, err := finishSyncCycle(pidPath, []string{path}, []syncRequest{req}, sigs)
	if err != nil {
		t.Fatal(err)
	}
	if !pending || terminate {
		t.Fatalf("finish = pending %v, terminate %v; want true, false", pending, terminate)
	}
	if _, err := os.Stat(pidPath); err != nil {
		t.Fatalf("worker retired despite SIGUSR2 received during sync: %v", err)
	}
}

func TestOpenSyncPIDDistinguishesStaleFileFromLiveLockedWorker(t *testing.T) {
	pidPath := filepath.Join(t.TempDir(), "sync.pid")
	if err := os.WriteFile(pidPath, []byte("424242\n"), 0600); err != nil {
		t.Fatal(err)
	}
	first, owned, err := openSyncPID(pidPath)
	if err != nil || !owned {
		t.Fatalf("stale pid open = owned %v, err %v; want owned", owned, err)
	}
	defer unlockSyncFile(first)
	if _, err := first.WriteAt([]byte("12345\n"), 0); err != nil {
		t.Fatal(err)
	}
	second, owned, err := openSyncPID(pidPath)
	if err != nil || owned {
		t.Fatalf("live pid open = owned %v, err %v; want not owned", owned, err)
	}
	defer second.Close()
	if pid, err := readPID(second); err != nil || pid != 12345 {
		t.Fatalf("read live pid = %d, %v; want 12345", pid, err)
	}
}

func TestIntegrationBackgroundSyncDebouncesAndRetires(t *testing.T) {
	bin := buildMD(t)
	h := newHarness(t)
	originDir := h.git("remote", "get-url", "origin")
	pidPath := filepath.Join(t.TempDir(), "meads-sync.pid")
	baseEnv := []string{
		"MEADS_SYNC_DISABLE=0",
		"MEADS_SYNC_PID=" + pidPath,
		"MEADS_SYNC_DELAY=10ms",
		"MEADS_SYNC_TIMEOUT=2s",
	}
	if out, err := runMD(t, bin, h.dir, baseEnv, "init", "--git"); err != nil {
		t.Fatalf("init --git: %v\n%s", err, out)
	}
	waitFor(t, 5*time.Second, func() bool {
		_, err := os.Stat(pidPath)
		return os.IsNotExist(err)
	}, "initial sync worker to retire")
	logPath := strings.TrimSuffix(pidPath, ".pid") + ".log"
	if err := os.Remove(logPath); err != nil && !os.IsNotExist(err) {
		t.Fatalf("clearing initialization sync log: %v", err)
	}

	env := []string{
		"MEADS_SYNC_DISABLE=0",
		"MEADS_SYNC_PID=" + pidPath,
		"MEADS_SYNC_DELAY=300ms",
		"MEADS_SYNC_TIMEOUT=2s",
	}
	started := time.Now()
	if out, err := runMD(t, bin, h.dir, env, "add", "first"); err != nil {
		t.Fatalf("first add: %v\n%s", err, out)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("background write blocked for %s", elapsed)
	}
	pid := readPIDPath(t, pidPath)
	assertDetachedSession(t, pid)

	time.Sleep(120 * time.Millisecond)
	if out, err := runMD(t, bin, h.dir, env, "add", "second"); err != nil {
		t.Fatalf("second add: %v\n%s", err, out)
	}
	time.Sleep(120 * time.Millisecond)
	if out, err := runMD(t, bin, h.dir, env, "add", "third"); err != nil {
		t.Fatalf("third add: %v\n%s", err, out)
	}
	// 120ms + 120ms + 180ms is past the first deadline but before the
	// third write's reset deadline.
	time.Sleep(180 * time.Millisecond)
	if refs := remoteRefNames(t, originDir); hasTaskRef(refs) {
		t.Fatalf("sync ran before reset debounce elapsed: %v", refs)
	}

	waitFor(t, 5*time.Second, func() bool {
		refs := remoteRefNames(t, originDir)
		count := 0
		for _, ref := range refs {
			if strings.HasPrefix(ref, meads.TasksRefPrefix) {
				count++
			}
		}
		return count == 3
	}, "all three task refs to reach origin")
	waitFor(t, 5*time.Second, func() bool {
		_, err := os.Stat(pidPath)
		return os.IsNotExist(err)
	}, "sync worker pid file to be removed")
	waitFor(t, 5*time.Second, func() bool {
		_, err := os.Stat(filepath.Join("/proc", strconv.Itoa(pid)))
		return os.IsNotExist(err)
	}, "sync worker to be reaped")
	logData, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("reading sync worker log: %v", err)
	}
	if got := strings.Count(string(logData), " synced in "); got != 1 {
		t.Fatalf("sync attempts after three debounced writes = %d, want exactly 1; log:\n%s", got, logData)
	}
}

func TestIntegrationBackgroundWorkerExitsIfRuntimeDirectoryDisappears(t *testing.T) {
	bin := buildMD(t)
	h := newHarness(t)
	runtimeDir := t.TempDir()
	pidPath := filepath.Join(runtimeDir, "meads-sync.pid")
	env := []string{
		"MEADS_SYNC_DISABLE=0",
		"MEADS_SYNC_PID=" + pidPath,
		"MEADS_SYNC_DELAY=50ms",
		"MEADS_SYNC_TIMEOUT=100ms",
	}
	if out, err := runMD(t, bin, h.dir, env, "init", "--git"); err != nil {
		t.Fatalf("init --git: %v\n%s", err, out)
	}
	pid := readPIDPath(t, pidPath)
	if err := os.RemoveAll(runtimeDir); err != nil {
		t.Fatalf("removing ephemeral runtime directory: %v", err)
	}
	waitFor(t, 3*time.Second, func() bool {
		_, err := os.Stat(filepath.Join("/proc", strconv.Itoa(pid)))
		return os.IsNotExist(err)
	}, "worker to exit after its runtime directory disappeared")
}

func waitFor(t *testing.T, timeout time.Duration, ok func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if ok() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func readPIDPath(t *testing.T, path string) int {
	t.Helper()
	var pid int
	waitFor(t, 2*time.Second, func() bool {
		data, err := os.ReadFile(path)
		if err != nil {
			return false
		}
		pid, err = strconv.Atoi(strings.TrimSpace(string(data)))
		return err == nil && pid > 1
	}, "worker pid")
	return pid
}

func assertDetachedSession(t *testing.T, pid int) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("/proc", strconv.Itoa(pid), "stat"))
	if err != nil {
		t.Fatalf("reading worker stat: %v", err)
	}
	// Fields after the closing command parenthesis: state, ppid, pgrp, sid.
	closeParen := strings.LastIndexByte(string(data), ')')
	if closeParen < 0 {
		t.Fatalf("malformed /proc stat: %s", data)
	}
	fields := strings.Fields(string(data[closeParen+1:]))
	if len(fields) < 4 {
		t.Fatalf("short /proc stat: %s", data)
	}
	sid, err := strconv.Atoi(fields[3])
	if err != nil {
		t.Fatal(err)
	}
	if sid != pid {
		t.Fatalf("worker session id = %d, want its own pid %d", sid, pid)
	}
	// In a normal Unix process tree the double-fork adopter is PID 1. Test
	// runners such as rais may deliberately install a child subreaper, which
	// becomes the kernel-selected adopter instead; either way the worker is no
	// longer a child of the short-lived md command and is reaped on exit.
}
