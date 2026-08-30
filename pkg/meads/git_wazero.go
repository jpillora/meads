package meads

import (
	"context"
	"errors"
	"os"
	"strconv"
	"strings"
	"sync"

	"github.com/jpillora/tigo"
)

// WazeroGit adapts tigo's embedded upstream Git runtime to Meads' Git
// interface. Meads delegates its local object/ref hot path and supported
// network operations; commands whose host-path semantics matter retain the
// native ExecGit fallback.
//
// The historical name is retained for API compatibility. New code that wants
// embedded Git directly should import github.com/jpillora/tigo.
type WazeroGit struct {
	Dir string

	native  *ExecGit
	once    sync.Once
	repo    *tigo.Repo
	initErr error
}

var (
	_ Git               = (*WazeroGit)(nil)
	_ ContextGit        = (*WazeroGit)(nil)
	_ CombinedOutputGit = (*WazeroGit)(nil)
)

// NewWazeroGit returns a lazy tigo-backed Meads Git adapter.
func NewWazeroGit(dir string) *WazeroGit {
	return &WazeroGit{Dir: dir, native: &ExecGit{Dir: dir}}
}

// Close releases the tigo repository and wazero runtime.
func (g *WazeroGit) Close(ctx context.Context) error {
	if g.repo == nil {
		return nil
	}
	return g.repo.Close(ctx)
}

// WASMCommandError is retained for callers that classified the old embedded
// backend's non-zero exits. New tigo users should use tigo.ExitError directly.
type WASMCommandError struct {
	Code   uint32
	Stderr string
	Cause  error
}

func (e *WASMCommandError) Error() string {
	message := "tigo exited with code " + strconv.FormatUint(uint64(e.Code), 10)
	if e.Stderr != "" {
		message += ": " + e.Stderr
	}
	return message
}

func (e *WASMCommandError) Unwrap() error { return e.Cause }

func (g *WazeroGit) initialize() {
	g.once.Do(func() {
		g.repo, g.initErr = tigo.OpenWithOptions(context.Background(), g.Dir, tigoOptionsFromEnv())
	})
}

func tigoOptionsFromEnv() tigo.Options {
	return tigo.Options{
		CacheDir:   os.Getenv("MEADS_WAZERO_CACHE"),
		GitDirOnly: true,
		HTTPAuth: tigo.HTTPAuth{
			Username: os.Getenv("MEADS_GIT_HTTP_USERNAME"),
			Password: os.Getenv("MEADS_GIT_HTTP_PASSWORD"),
			Token:    os.Getenv("MEADS_GIT_HTTP_TOKEN"),
		},
		SSHAuth: tigo.SSHAuth{
			PrivateKeyPath: os.Getenv("MEADS_GIT_SSH_KEY"),
			Passphrase:     os.Getenv("MEADS_GIT_SSH_PASSPHRASE"),
		},
	}
}

// wasmGitCommand is Meads' narrower delegation set. Tigo itself exposes more
// compiled builtins, but several return WASI guest paths or touch the mounted
// worktree and therefore are not transparent replacements for ExecGit here.
func wasmGitCommand(args []string) bool {
	commandPos := 0
	for commandPos+1 < len(args) && args[commandPos] == "-c" {
		commandPos += 2
	}
	if commandPos >= len(args) {
		return false
	}
	switch args[commandPos] {
	case "hash-object", "mktree", "commit-tree", "for-each-ref", "cat-file", "rev-list", "update-ref":
		return true
	default:
		return false
	}
}

func remoteGitCommand(args []string) bool {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return false
	}
	switch args[0] {
	case "fetch", "push", "ls-remote":
		return true
	case "remote":
		return len(args) > 1 && args[1] == "get-url"
	default:
		return false
	}
}

func (g *WazeroGit) shouldDelegate(args []string) bool {
	return wasmGitCommand(args) || remoteGitCommand(args)
}

func (g *WazeroGit) command(args ...string) (*tigo.Cmd, error) {
	g.initialize()
	if g.initErr != nil {
		return nil, g.initErr
	}
	return g.repo.Command(args...), nil
}

func adapterError(err error) error {
	var exitErr *tigo.ExitError
	if !errors.As(err, &exitErr) {
		return err
	}
	return &WASMCommandError{
		Code: exitErr.Code, Stderr: strings.TrimSpace(string(exitErr.Stderr)), Cause: err,
	}
}

func (g *WazeroGit) Run(args ...string) error {
	return g.RunContext(context.Background(), args...)
}

func (g *WazeroGit) RunContext(ctx context.Context, args ...string) error {
	if !g.shouldDelegate(args) {
		return g.native.RunContext(ctx, args...)
	}
	cmd, err := g.command(args...)
	if err != nil {
		return err
	}
	err = cmd.Run(ctx)
	if errors.Is(err, tigo.ErrUnsupported) {
		return g.native.RunContext(ctx, args...)
	}
	return adapterError(err)
}

func (g *WazeroGit) Output(args ...string) (string, error) {
	return g.OutputContext(context.Background(), args...)
}

func (g *WazeroGit) OutputContext(ctx context.Context, args ...string) (string, error) {
	if !g.shouldDelegate(args) {
		return g.native.OutputContext(ctx, args...)
	}
	cmd, err := g.command(args...)
	if err != nil {
		return "", err
	}
	out, err := cmd.Output(ctx)
	if errors.Is(err, tigo.ErrUnsupported) {
		return g.native.OutputContext(ctx, args...)
	}
	if err != nil {
		return "", adapterError(err)
	}
	return strings.TrimSpace(string(out)), nil
}

func (g *WazeroGit) OutputWithInput(stdin string, args ...string) (string, error) {
	out, err := g.outputRawWithInput(context.Background(), stdin, args...)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func (g *WazeroGit) OutputRaw(args ...string) ([]byte, error) {
	return g.outputRawWithInput(context.Background(), "", args...)
}

func (g *WazeroGit) OutputRawWithInput(stdin string, args ...string) ([]byte, error) {
	return g.outputRawWithInput(context.Background(), stdin, args...)
}

func (g *WazeroGit) outputRawWithInput(ctx context.Context, stdin string, args ...string) ([]byte, error) {
	if !g.shouldDelegate(args) {
		if stdin == "" {
			return g.native.OutputRaw(args...)
		}
		return g.native.OutputRawWithInput(stdin, args...)
	}
	cmd, err := g.command(args...)
	if err != nil {
		return nil, err
	}
	cmd.Stdin = strings.NewReader(stdin)
	out, err := cmd.Output(ctx)
	if errors.Is(err, tigo.ErrUnsupported) {
		if stdin == "" {
			return g.native.OutputRaw(args...)
		}
		return g.native.OutputRawWithInput(stdin, args...)
	}
	return out, adapterError(err)
}

func (g *WazeroGit) CombinedOutputContext(ctx context.Context, args ...string) (string, error) {
	if !g.shouldDelegate(args) {
		return g.native.CombinedOutputContext(ctx, args...)
	}
	cmd, err := g.command(args...)
	if err != nil {
		return "", err
	}
	out, err := cmd.CombinedOutput(ctx)
	if errors.Is(err, tigo.ErrUnsupported) {
		return g.native.CombinedOutputContext(ctx, args...)
	}
	return string(out), adapterError(err)
}
