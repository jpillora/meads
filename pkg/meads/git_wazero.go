package meads

import (
	"bytes"
	"context"
	"crypto/rand"
	_ "embed"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/tetratelabs/wazero"
	"github.com/tetratelabs/wazero/imports/wasi_snapshot_preview1"
	"github.com/tetratelabs/wazero/sys"
)

//go:embed wasmgit.wasm
var wazeroGitModule []byte

// WazeroGit implements Git with the wasm-git/libgit2 WASI module for Meads'
// local object and ref plumbing. Commands outside that deliberately small set
// (notably fetch, push and repository discovery) fall back to ExecGit.
//
// Each command gets a fresh WASI instance, while the expensive WebAssembly
// compilation is shared for the life of WazeroGit. The instance can access
// only the repository's Git directory, mounted read/write at /git; it never
// receives the process environment or the working tree.
type WazeroGit struct {
	Dir string

	native  *ExecGit
	once    sync.Once
	rt      wazero.Runtime
	module  wazero.CompiledModule
	cache   wazero.CompilationCache
	mount   string
	gitDir  string
	initErr error
}

var (
	_ Git               = (*WazeroGit)(nil)
	_ ContextGit        = (*WazeroGit)(nil)
	_ CombinedOutputGit = (*WazeroGit)(nil)
)

// NewWazeroGit returns a hybrid Git implementation whose local Meads plumbing
// runs in wazero. Initialization and compilation are lazy, so construction is
// cheap and an unsupported command does not start the WebAssembly runtime.
func NewWazeroGit(dir string) *WazeroGit {
	return &WazeroGit{Dir: dir, native: &ExecGit{Dir: dir}}
}

// Close releases compiled WebAssembly code and the wazero runtime. A short
// lived md process may let process exit do this; benchmarks and long-lived
// library users should close explicitly.
func (g *WazeroGit) Close(ctx context.Context) error {
	var errs []error
	if g.rt != nil {
		errs = append(errs, g.rt.Close(ctx))
	}
	if g.cache != nil {
		errs = append(errs, g.cache.Close(ctx))
	}
	return errors.Join(errs...)
}

// WASMCommandError is returned when the embedded command exits non-zero.
type WASMCommandError struct {
	Code   uint32
	Stderr string
	Cause  error
}

func (e *WASMCommandError) Error() string {
	message := fmt.Sprintf("wasm-git exited with code %d", e.Code)
	if e.Stderr != "" {
		message += ": " + e.Stderr
	}
	return message
}

func (e *WASMCommandError) Unwrap() error { return e.Cause }

func (g *WazeroGit) initialize() {
	g.once.Do(func() {
		g.mount, g.gitDir, g.initErr = discoverGitMount(g.Dir)
		if g.initErr != nil {
			return
		}
		ctx := context.Background()
		runtimeConfig := wazero.NewRuntimeConfig()
		if cacheDir, err := wazeroGitCacheDir(); err == nil {
			g.cache, err = wazero.NewCompilationCacheWithDir(cacheDir)
			if err != nil {
				g.initErr = fmt.Errorf("open wazero compilation cache: %w", err)
				return
			}
			runtimeConfig = runtimeConfig.WithCompilationCache(g.cache)
		}
		g.rt = wazero.NewRuntimeWithConfig(ctx, runtimeConfig)
		if _, err := wasi_snapshot_preview1.Instantiate(ctx, g.rt); err != nil {
			g.initErr = fmt.Errorf("instantiate WASI: %w", err)
			return
		}
		g.module, g.initErr = g.rt.CompileModule(ctx, wazeroGitModule)
		if g.initErr != nil {
			g.initErr = fmt.Errorf("compile wasm-git: %w", g.initErr)
		}
	})
}

func wazeroGitCacheDir() (string, error) {
	if dir := os.Getenv("MEADS_WAZERO_CACHE"); dir != "" {
		return dir, nil
	}
	root, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(root, "meads", "wazero"), nil
}

// discoverGitMount resolves .git without invoking Git. For linked worktrees it
// mounts the common Git directory and opens the worktree's administrative
// directory beneath it, preserving the relative commondir link.
func discoverGitMount(dir string) (mount, guestGitDir string, err error) {
	if dir == "" {
		dir, err = os.Getwd()
		if err != nil {
			return "", "", err
		}
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		return "", "", err
	}
	for current := dir; ; current = filepath.Dir(current) {
		candidate := filepath.Join(current, ".git")
		info, statErr := os.Stat(candidate)
		if statErr == nil {
			gitDir := candidate
			if !info.IsDir() {
				data, readErr := os.ReadFile(candidate)
				if readErr != nil {
					return "", "", fmt.Errorf("read %s: %w", candidate, readErr)
				}
				const prefix = "gitdir:"
				value := strings.TrimSpace(string(data))
				if !strings.HasPrefix(strings.ToLower(value), prefix) {
					return "", "", fmt.Errorf("invalid gitdir file %s", candidate)
				}
				gitDir = strings.TrimSpace(value[len(prefix):])
				if !filepath.IsAbs(gitDir) {
					gitDir = filepath.Join(current, gitDir)
				}
				gitDir = filepath.Clean(gitDir)
			}
			return gitMountForDir(gitDir)
		}
		if statErr != nil && !errors.Is(statErr, os.ErrNotExist) {
			return "", "", fmt.Errorf("stat %s: %w", candidate, statErr)
		}
		if isBareGitDir(current) {
			return gitMountForDir(current)
		}
		parent := filepath.Dir(current)
		if parent == current {
			break
		}
	}
	return "", "", fmt.Errorf("not a git repository: %s", dir)
}

func isBareGitDir(dir string) bool {
	if info, err := os.Stat(filepath.Join(dir, "HEAD")); err != nil || info.IsDir() {
		return false
	}
	info, err := os.Stat(filepath.Join(dir, "objects"))
	return err == nil && info.IsDir()
}

func gitMountForDir(gitDir string) (mount, guestGitDir string, err error) {
	gitDir, err = filepath.Abs(gitDir)
	if err != nil {
		return "", "", err
	}
	commondirPath := filepath.Join(gitDir, "commondir")
	data, readErr := os.ReadFile(commondirPath)
	if errors.Is(readErr, os.ErrNotExist) {
		return gitDir, "/git", nil
	}
	if readErr != nil {
		return "", "", fmt.Errorf("read %s: %w", commondirPath, readErr)
	}
	common := strings.TrimSpace(string(data))
	if !filepath.IsAbs(common) {
		common = filepath.Join(gitDir, common)
	}
	common = filepath.Clean(common)
	rel, err := filepath.Rel(common, gitDir)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", "", fmt.Errorf("git dir %s is outside common dir %s", gitDir, common)
	}
	return common, filepath.ToSlash(filepath.Join("/git", rel)), nil
}

func wasmGitCommand(args []string) bool {
	commandPos := 0
	for commandPos+1 < len(args) && args[commandPos] == "-c" {
		commandPos += 2
	}
	if commandPos >= len(args) {
		return false
	}
	switch args[commandPos] {
	case "hash-object", "mktree", "commit-tree", "for-each-ref", "cat-file", "rev-list":
		return true
	case "update-ref":
		// libgit2 locks every ref before committing a transaction, but its
		// multi-ref commit can still stop partway through on an I/O failure.
		// Native `git update-ref --stdin` provides Meads' stronger all-or-none
		// guarantee, so retain it for batch updates. Single-ref CAS is atomic
		// in libgit2 and is the mutation hot path measured by this experiment.
		for _, arg := range args[commandPos+1:] {
			if arg == "--stdin" {
				return false
			}
		}
		return true
	default:
		return false
	}
}

func (g *WazeroGit) execute(ctx context.Context, stdin string, args ...string) ([]byte, string, error) {
	g.initialize()
	if g.initErr != nil {
		return nil, "", g.initErr
	}
	var stdout, stderr bytes.Buffer
	moduleArgs := make([]string, 1, len(args)+1)
	moduleArgs[0] = "git"
	moduleArgs = append(moduleArgs, args...)
	config := wazero.NewModuleConfig().
		WithName("").
		WithArgs(moduleArgs...).
		WithEnv("HOME", "/git").
		WithEnv("MEADS_GIT_DIR", g.gitDir).
		WithStdin(strings.NewReader(stdin)).
		WithStdout(&stdout).
		WithStderr(&stderr).
		WithRandSource(rand.Reader).
		WithSysWalltime().
		WithSysNanotime().
		WithFSConfig(wazero.NewFSConfig().WithDirMount(g.mount, "/git"))
	instance, err := g.rt.InstantiateModule(ctx, g.module, config)
	if instance != nil {
		_ = instance.Close(ctx)
	}
	if err == nil {
		return stdout.Bytes(), stderr.String(), nil
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return nil, stderr.String(), ctxErr
	}
	var exitErr *sys.ExitError
	if errors.As(err, &exitErr) {
		return nil, stderr.String(), &WASMCommandError{
			Code: exitErr.ExitCode(), Stderr: strings.TrimSpace(stderr.String()), Cause: err,
		}
	}
	return nil, stderr.String(), fmt.Errorf("run wasm-git: %w", err)
}

func (g *WazeroGit) Run(args ...string) error {
	return g.RunContext(context.Background(), args...)
}

func (g *WazeroGit) RunContext(ctx context.Context, args ...string) error {
	if !wasmGitCommand(args) {
		return g.native.RunContext(ctx, args...)
	}
	_, _, err := g.execute(ctx, "", args...)
	return err
}

func (g *WazeroGit) Output(args ...string) (string, error) {
	return g.OutputContext(context.Background(), args...)
}

func (g *WazeroGit) OutputContext(ctx context.Context, args ...string) (string, error) {
	if !wasmGitCommand(args) {
		return g.native.OutputContext(ctx, args...)
	}
	out, _, err := g.execute(ctx, "", args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (g *WazeroGit) OutputWithInput(stdin string, args ...string) (string, error) {
	if !wasmGitCommand(args) {
		return g.native.OutputWithInput(stdin, args...)
	}
	out, _, err := g.execute(context.Background(), stdin, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (g *WazeroGit) OutputRaw(args ...string) ([]byte, error) {
	if !wasmGitCommand(args) {
		return g.native.OutputRaw(args...)
	}
	out, _, err := g.execute(context.Background(), "", args...)
	return out, err
}

func (g *WazeroGit) OutputRawWithInput(stdin string, args ...string) ([]byte, error) {
	if !wasmGitCommand(args) {
		return g.native.OutputRawWithInput(stdin, args...)
	}
	out, _, err := g.execute(context.Background(), stdin, args...)
	return out, err
}

func (g *WazeroGit) CombinedOutputContext(ctx context.Context, args ...string) (string, error) {
	if !wasmGitCommand(args) {
		return g.native.CombinedOutputContext(ctx, args...)
	}
	out, stderr, err := g.execute(ctx, "", args...)
	return string(out) + stderr, err
}
