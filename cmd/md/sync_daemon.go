package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const syncDaemonEnv = "MEADS_SYNC_DAEMON"

type syncRequest struct {
	RepoDir    string `json:"repo_dir"`
	CommonDir  string `json:"common_dir"`
	Generation uint64 `json:"generation"`
	Delay      string `json:"delay"`
	Timeout    string `json:"timeout,omitempty"`
	Verbose    bool   `json:"verbose,omitempty"`
}

func defaultSyncPIDPath() (string, error) {
	if path := strings.TrimSpace(os.Getenv("MEADS_SYNC_PID")); path != "" {
		if filepath.IsAbs(path) {
			return filepath.Clean(path), nil
		}
		abs, err := filepath.Abs(path)
		return filepath.Clean(abs), err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolving home directory for MEADS_SYNC_PID: %w", err)
	}
	return filepath.Join(home, ".local", "run", "meads-sync.pid"), nil
}

func syncRequestDir(pidPath string) string { return pidPath + ".d" }
func syncLockPath(pidPath string) string   { return pidPath + ".lock" }

func syncRequestPath(pidPath, commonDir string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(commonDir)))
	return filepath.Join(syncRequestDir(pidPath), hex.EncodeToString(sum[:])[:24]+".json")
}

func readSyncRequest(path string) (syncRequest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return syncRequest{}, err
	}
	var req syncRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return syncRequest{}, err
	}
	if req.RepoDir == "" || req.CommonDir == "" || req.Generation == 0 {
		return syncRequest{}, fmt.Errorf("incomplete sync request")
	}
	return req, nil
}

func writeSyncRequest(pidPath string, req syncRequest) error {
	dir := syncRequestDir(pidPath)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	path := syncRequestPath(pidPath, req.CommonDir)
	if old, err := readSyncRequest(path); err == nil && old.Generation >= req.Generation {
		req.Generation = old.Generation + 1
	}
	if req.Generation == 0 {
		req.Generation = 1
	}
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".request-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(0600); err == nil {
		_, err = tmp.Write(append(data, '\n'))
	}
	if err == nil {
		err = tmp.Sync()
	}
	if closeErr := tmp.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func listSyncRequests(pidPath string) ([]string, []syncRequest, error) {
	paths, err := filepath.Glob(filepath.Join(syncRequestDir(pidPath), "*.json"))
	if err != nil {
		return nil, nil, err
	}
	requests := make([]syncRequest, 0, len(paths))
	validPaths := make([]string, 0, len(paths))
	for _, path := range paths {
		req, err := readSyncRequest(path)
		if err != nil {
			continue
		}
		validPaths = append(validPaths, path)
		requests = append(requests, req)
	}
	return validPaths, requests, nil
}

func requestDelay(req syncRequest, fallback time.Duration) time.Duration {
	if d, err := time.ParseDuration(req.Delay); err == nil && d >= 0 {
		return d
	}
	return fallback
}

func pendingSyncDelay(pidPath string, fallback time.Duration) time.Duration {
	_, requests, _ := listSyncRequests(pidPath)
	delay := fallback
	for i, req := range requests {
		d := requestDelay(req, fallback)
		if i == 0 || d < delay {
			delay = d
		}
	}
	return delay
}
