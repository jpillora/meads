//go:build unix

package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const (
	syncDaemonHelper = "helper"
	syncDaemonWorker = "worker"
)

func enqueueBackgroundSync(g *globals, commonDir string, delay, timeout time.Duration) error {
	pidPath, err := defaultSyncPIDPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(pidPath), 0700); err != nil {
		return fmt.Errorf("creating sync runtime directory: %w", err)
	}

	registration, err := lockSyncRegistration(pidPath)
	if err != nil {
		return err
	}
	defer unlockSyncFile(registration)

	repoDir := g.Dir
	if repoDir == "" {
		repoDir, err = os.Getwd()
		if err != nil {
			return err
		}
	}
	if repoDir, err = filepath.Abs(repoDir); err != nil {
		return err
	}
	generation := uint64(time.Now().UnixNano())
	if generation == 0 {
		generation = 1
	}
	req := syncRequest{
		RepoDir:    repoDir,
		CommonDir:  commonDir,
		Generation: generation,
		Delay:      delay.String(),
		Verbose:    g.Verbose,
	}
	if timeout > 0 {
		req.Timeout = timeout.String()
	}
	if err := writeSyncRequest(pidPath, req); err != nil {
		return fmt.Errorf("queueing sync request: %w", err)
	}

	pidFile, owned, err := openSyncPID(pidPath)
	if err != nil {
		return err
	}
	defer pidFile.Close()
	if !owned {
		pid, err := readPID(pidFile)
		if err != nil {
			return fmt.Errorf("reading live sync worker pid: %w", err)
		}
		if err := syscall.Kill(pid, syscall.SIGUSR2); err != nil {
			return fmt.Errorf("signalling sync worker %d: %w", pid, err)
		}
		g.verbosef("background sync queued; reset worker %d debounce timer (%s)\n", pid, delay)
		return nil
	}

	if err := startSyncHelper(g, pidPath, pidFile, delay, timeout); err != nil {
		return err
	}
	pid, _ := readPID(pidFile)
	g.verbosef("background sync queued; started worker %d (delay %s, pid %s)\n", pid, delay, pidPath)
	return nil
}

func syncDaemonDispatch(_ *globals) (bool, error) {
	switch os.Getenv(syncDaemonEnv) {
	case "":
		return false, nil
	case syncDaemonHelper:
		return true, runSyncHelper()
	case syncDaemonWorker:
		return true, runSyncWorker()
	default:
		return true, fmt.Errorf("invalid %s mode", syncDaemonEnv)
	}
}

func lockSyncRegistration(pidPath string) (*os.File, error) {
	f, err := os.OpenFile(syncLockPath(pidPath), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("opening sync registration lock: %w", err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		f.Close()
		return nil, fmt.Errorf("locking sync registration: %w", err)
	}
	return f, nil
}

func unlockSyncFile(f *os.File) {
	if f == nil {
		return
	}
	_ = unix.Flock(int(f.Fd()), unix.LOCK_UN)
	_ = f.Close()
}

// openSyncPID returns owned=true when the caller acquired the lifetime lock
// and must start a worker. A failed non-blocking flock means the inode is held
// by our live worker, making the PID inside safe to signal (not a reused PID).
func openSyncPID(pidPath string) (*os.File, bool, error) {
	f, err := os.OpenFile(pidPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, false, fmt.Errorf("opening sync pid file: %w", err)
	}
	err = unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB)
	if err == nil {
		if err := f.Truncate(0); err != nil {
			unlockSyncFile(f)
			return nil, false, err
		}
		return f, true, nil
	}
	if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
		return f, false, nil
	}
	f.Close()
	return nil, false, fmt.Errorf("locking sync pid file: %w", err)
}

func readPID(f *os.File) (int, error) {
	buf := make([]byte, 64)
	n, err := f.ReadAt(buf, 0)
	if err != nil && !errors.Is(err, io.EOF) {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(buf[:n])))
	if err != nil || pid <= 1 {
		return 0, fmt.Errorf("invalid pid %q", strings.TrimSpace(string(buf[:n])))
	}
	return pid, nil
}

func startSyncHelper(g *globals, pidPath string, pidFile *os.File, delay, timeout time.Duration) error {
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.Command(exe, "sync")
	cmd.Dir = g.Dir
	cmd.ExtraFiles = []*os.File{pidFile}
	cmd.Env = syncWorkerEnv(os.Environ(), syncDaemonHelper, pidPath, delay, timeout)
	cmd.Stdin = nil
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("starting sync helper: %w", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("sync helper: %w", err)
		}
		return nil
	case <-time.After(3 * time.Second):
		_ = cmd.Process.Kill()
		<-done
		return errors.New("sync helper did not start the worker within 3s")
	}
}

func syncWorkerEnv(base []string, mode, pidPath string, delay, timeout time.Duration) []string {
	env := append([]string(nil), base...)
	env = setEnv(env, syncDaemonEnv, mode)
	env = setEnv(env, "MEADS_SYNC_PID", pidPath)
	env = setEnv(env, "MEADS_SYNC_DELAY", delay.String())
	if timeout > 0 {
		env = setEnv(env, "MEADS_SYNC_TIMEOUT", timeout.String())
	} else {
		env = setEnv(env, "MEADS_SYNC_TIMEOUT", "0")
	}
	return env
}

func setEnv(env []string, key, value string) []string {
	prefix := key + "="
	for i := range env {
		if strings.HasPrefix(env[i], prefix) {
			env[i] = prefix + value
			return env
		}
	}
	return append(env, prefix+value)
}

// runSyncHelper performs the second fork. The scheduling CLI waits for and
// reaps this helper; the helper starts a new session, waits until the worker
// has written its PID, then exits. The worker is therefore orphaned and reaped
// by PID 1 (or the nearest process marked as a child subreaper) when it
// eventually retires, never left as the CLI's zombie child.
func runSyncHelper() error {
	pidFile := os.NewFile(uintptr(3), "meads-sync.pid")
	if pidFile == nil {
		return errors.New("sync helper did not receive the locked pid file")
	}
	defer pidFile.Close()
	readyR, readyW, err := os.Pipe()
	if err != nil {
		return err
	}
	defer readyR.Close()

	exe, err := os.Executable()
	if err != nil {
		readyW.Close()
		return err
	}
	pidPath, err := defaultSyncPIDPath()
	if err != nil {
		readyW.Close()
		return err
	}
	logPath := strings.TrimSpace(os.Getenv("MEADS_SYNC_LOG"))
	if logPath == "" {
		logPath = strings.TrimSuffix(pidPath, ".pid") + ".log"
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0700); err != nil {
		readyW.Close()
		return err
	}
	logFile, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		readyW.Close()
		return err
	}
	defer logFile.Close()
	null, err := os.OpenFile(os.DevNull, os.O_RDONLY, 0)
	if err != nil {
		readyW.Close()
		return err
	}
	defer null.Close()

	cmd := exec.Command(exe, "sync")
	cmd.Env = setEnv(os.Environ(), syncDaemonEnv, syncDaemonWorker)
	cmd.ExtraFiles = []*os.File{pidFile, readyW}
	cmd.Stdin = null
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		readyW.Close()
		return err
	}
	readyW.Close()

	ready := make(chan error, 1)
	go func() {
		var one [1]byte
		_, err := readyR.Read(one[:])
		ready <- err
	}()
	select {
	case err := <-ready:
		if err != nil {
			_ = cmd.Process.Kill()
			return fmt.Errorf("sync worker startup: %w", err)
		}
		return cmd.Process.Release()
	case <-time.After(2 * time.Second):
		_ = cmd.Process.Kill()
		return errors.New("sync worker did not publish its pid within 2s")
	}
}

func runSyncWorker() error {
	pidFile := os.NewFile(uintptr(3), "meads-sync.pid")
	if pidFile == nil {
		return errors.New("sync worker did not receive the locked pid file")
	}
	defer pidFile.Close()
	ready := os.NewFile(uintptr(4), "meads-sync.ready")
	if ready == nil {
		return errors.New("sync worker did not receive its readiness pipe")
	}
	pidPath, err := defaultSyncPIDPath()
	if err != nil {
		ready.Close()
		return err
	}
	if err := pidFile.Truncate(0); err != nil {
		ready.Close()
		return err
	}
	if _, err := pidFile.WriteAt([]byte(strconv.Itoa(os.Getpid())+"\n"), 0); err != nil {
		ready.Close()
		return err
	}
	_ = pidFile.Sync()
	_, _ = ready.Write([]byte{1})
	ready.Close()

	fallbackDelay, err := time.ParseDuration(os.Getenv("MEADS_SYNC_DELAY"))
	if err != nil || fallbackDelay < 0 {
		fallbackDelay = time.Minute
	}
	signals := make(chan os.Signal, 32)
	signal.Notify(signals, syscall.SIGUSR2, syscall.SIGTERM, syscall.SIGINT)
	defer signal.Stop(signals)
	defer retireSyncWorker(pidPath, pidFile)

	for {
		delay := pendingSyncDelay(pidPath, fallbackDelay)
		timer := time.NewTimer(delay)
	debounce:
		for {
			select {
			case sig := <-signals:
				if sig == syscall.SIGTERM || sig == syscall.SIGINT {
					stopTimer(timer)
					return nil
				}
				stopTimer(timer)
				delay = pendingSyncDelay(pidPath, fallbackDelay)
				timer.Reset(delay)
			case <-timer.C:
				break debounce
			}
		}

		paths, requests, _ := listSyncRequests(pidPath)
		for _, req := range requests {
			runQueuedSync(req)
		}

		// Signals can coalesce, so request generations—not signal counts—are
		// authoritative. A queued generation newer than our snapshot remains
		// on disk and forces another full debounce after this sync.
		pending, terminate, err := finishSyncCycle(pidPath, paths, requests, signals)
		if err != nil {
			fmt.Fprintf(os.Stderr, "meads sync worker: finishing cycle: %v\n", err)
			// The runtime directory may have been removed (for example, an
			// ephemeral checkout disappeared while the timer was running). With
			// no registration lock there is no safe state transition to retry;
			// retire and let a later write recreate the queue and worker.
			return nil
		}
		if terminate {
			return nil
		}
		if !pending {
			return nil
		}
	}
}

func stopTimer(timer *time.Timer) {
	if !timer.Stop() {
		select {
		case <-timer.C:
		default:
		}
	}
}

func runQueuedSync(req syncRequest) {
	started := time.Now()
	g := &globals{
		Dir:           req.RepoDir,
		TasksFile:     "TASKS.md",
		GitMode:       true,
		Verbose:       req.Verbose,
		VerboseOutput: os.Stderr,
	}
	ctx := context.Background()
	if timeout, err := time.ParseDuration(req.Timeout); err == nil && timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	report, err := syncFunc(ctx, g)
	renderSyncReport(report)
	if err != nil {
		fmt.Fprintf(os.Stderr, "meads sync worker: %s failed after %s: %v\n", req.RepoDir, time.Since(started).Round(time.Millisecond), err)
		return
	}
	fmt.Fprintf(os.Stderr, "meads sync worker: %s synced in %s\n", req.RepoDir, time.Since(started).Round(time.Millisecond))
}

func finishSyncCycle(pidPath string, paths []string, snapshots []syncRequest, signals <-chan os.Signal) (pending, terminate bool, err error) {
	registration, err := lockSyncRegistration(pidPath)
	if err != nil {
		return true, false, err
	}
	defer unlockSyncFile(registration)
	for i, path := range paths {
		current, err := readSyncRequest(path)
		if err == nil && current.Generation == snapshots[i].Generation {
			_ = os.Remove(path)
		}
	}
	all, err := filepath.Glob(filepath.Join(syncRequestDir(pidPath), "*.json"))
	if err != nil {
		return true, false, err
	}
	for _, path := range all {
		if _, err := readSyncRequest(path); err != nil {
			_ = os.Remove(path)
		}
	}
	valid, _, err := listSyncRequests(pidPath)
	if err != nil {
		return true, false, err
	}
	pending = len(valid) > 0
	// A signal received while Sync was blocking must also force another
	// debounce cycle, even when it was sent manually without a request file.
	// Writers additionally update a durable generation, so signal coalescing
	// and the tiny signal-vs-retirement edge cannot lose their work.
	for {
		select {
		case sig := <-signals:
			if sig == syscall.SIGTERM || sig == syscall.SIGINT {
				terminate = true
			} else {
				pending = true
			}
		default:
			if !pending || terminate {
				_ = os.Remove(pidPath)
			}
			return pending, terminate, nil
		}
	}
}

func retireSyncWorker(pidPath string, pidFile *os.File) {
	registration, err := lockSyncRegistration(pidPath)
	if err != nil {
		return
	}
	defer unlockSyncFile(registration)
	// The clean-retirement path may already have unlinked this inode, allowing
	// a writer to create and lock a replacement before this defer runs. Never
	// remove that new worker's PID file: unlink only when the pathname still
	// identifies our inherited, locked file description.
	lockedInfo, lockedErr := pidFile.Stat()
	pathInfo, pathErr := os.Stat(pidPath)
	if lockedErr == nil && pathErr == nil && os.SameFile(lockedInfo, pathInfo) {
		_ = os.Remove(pidPath)
	}
	_ = unix.Flock(int(pidFile.Fd()), unix.LOCK_UN)
}
