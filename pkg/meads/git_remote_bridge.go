package meads

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/go-git/go-billy/v5/osfs"
	git "github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/config"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/cache"
	"github.com/go-git/go-git/v5/plumbing/transport"
	httpauth "github.com/go-git/go-git/v5/plumbing/transport/http"
	sshauth "github.com/go-git/go-git/v5/plumbing/transport/ssh"
	"github.com/go-git/go-git/v5/storage/filesystem"
	gossh "golang.org/x/crypto/ssh"
)

// remoteBridgeResult is the result of a Git command handled by the Go host
// transport. handled is false when the command or URL scheme is outside the
// deliberately small bridge surface, in which case WazeroGit retains its
// native Git fallback.
type remoteBridgeResult struct {
	stdout  string
	stderr  string
	handled bool
	err     error
}

// remoteBridgeCommand runs network operations in the Go host. Upstream Git's
// HTTP and SSH transports are separate helper processes, and fetch/push spawn
// more Git processes for pack indexing/generation. WASI Preview 1 has neither
// outbound sockets nor fork/exec, so the host owns the complete remote
// transfer while the embedded C Git remains responsible for local plumbing.
func (g *WazeroGit) remoteBridgeCommand(ctx context.Context, args ...string) remoteBridgeResult {
	command, commandArgs, ok := plainGitCommand(args)
	if !ok {
		return remoteBridgeResult{}
	}

	switch command {
	case "remote":
		return g.bridgeRemoteGetURL(commandArgs)
	case "fetch", "push", "ls-remote":
		// handled below
	default:
		return remoteBridgeResult{}
	}

	repo, err := g.bridgeRepository()
	if err != nil {
		return remoteBridgeResult{handled: true, err: fmt.Errorf("open host transport repository: %w", err)}
	}

	switch command {
	case "fetch":
		return bridgeFetch(ctx, repo, commandArgs)
	case "push":
		return bridgePush(ctx, repo, commandArgs)
	case "ls-remote":
		return bridgeList(ctx, repo, commandArgs)
	default:
		panic("unreachable remote bridge command")
	}
}

// plainGitCommand recognizes commands with no global Git options. In
// particular, -c affects transport behavior and cannot be silently ignored by
// the Go implementation, so such invocations stay on the native fallback.
func plainGitCommand(args []string) (command string, commandArgs []string, ok bool) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return "", nil, false
	}
	return args[0], args[1:], true
}

func (g *WazeroGit) bridgeRepository() (*git.Repository, error) {
	mount, _, err := discoverGitMount(g.Dir)
	if err != nil {
		return nil, err
	}
	storage := filesystem.NewStorage(osfs.New(mount), cache.NewObjectLRUDefault())
	return git.Open(storage, nil)
}

func (g *WazeroGit) bridgeRemoteGetURL(args []string) remoteBridgeResult {
	push, all, name, ok := parseRemoteGetURL(args)
	if !ok {
		return remoteBridgeResult{}
	}
	repo, err := g.bridgeRepository()
	if err != nil {
		return remoteBridgeResult{handled: true, err: fmt.Errorf("open host transport repository: %w", err)}
	}
	remote, err := repo.Remote(name)
	if err != nil {
		return remoteBridgeResult{handled: true, err: err}
	}
	urls := remote.Config().URLs
	if len(urls) == 0 {
		return remoteBridgeResult{handled: true, err: fmt.Errorf("remote %q has no URLs", name)}
	}
	if push {
		urls = urls[len(urls)-1:]
	} else if !all {
		urls = urls[:1]
	}
	return remoteBridgeResult{handled: true, stdout: strings.Join(urls, "\n")}
}

func parseRemoteGetURL(args []string) (push, all bool, name string, ok bool) {
	if len(args) < 2 || args[0] != "get-url" {
		return false, false, "", false
	}
	for _, arg := range args[1:] {
		switch arg {
		case "--push":
			push = true
		case "--all":
			all = true
		default:
			if strings.HasPrefix(arg, "-") || name != "" {
				return false, false, "", false
			}
			name = arg
		}
	}
	return push, all, name, name != ""
}

func bridgeFetch(ctx context.Context, repo *git.Repository, args []string) remoteBridgeResult {
	remoteName, refspecs, opts, ok := parseFetch(args)
	if !ok {
		return remoteBridgeResult{}
	}
	remote, supported, err := bridgedRemote(repo, remoteName, false)
	if err != nil {
		return remoteBridgeResult{handled: true, err: err}
	}
	if !supported {
		return remoteBridgeResult{}
	}
	auth, err := bridgeAuth(ctx, remote.Config().URLs[0])
	if err != nil {
		return remoteBridgeResult{handled: true, err: err}
	}
	opts.RemoteName = remote.Config().Name
	opts.RefSpecs = refspecs
	opts.Auth = auth
	var progress bytes.Buffer
	opts.Progress = &progress
	err = remote.FetchContext(ctx, opts)
	if errors.Is(err, git.NoErrAlreadyUpToDate) {
		err = nil
	}
	return remoteBridgeResult{handled: true, stderr: progress.String(), err: err}
}

func parseFetch(args []string) (string, []config.RefSpec, *git.FetchOptions, bool) {
	opts := &git.FetchOptions{}
	remoteName := "origin"
	seenRemote := false
	var refspecs []config.RefSpec
	for _, arg := range args {
		switch arg {
		case "--force", "-f":
			opts.Force = true
		case "--prune", "-p":
			opts.Prune = true
		case "--tags", "-t":
			opts.Tags = git.AllTags
		case "--no-tags", "-n":
			opts.Tags = git.NoTags
		case "--quiet", "-q", "--verbose", "-v":
			// Output selection only; the bridge captures progress either way.
		default:
			if strings.HasPrefix(arg, "-") {
				return "", nil, nil, false
			}
			if !seenRemote {
				remoteName, seenRemote = arg, true
			} else {
				refspecs = append(refspecs, config.RefSpec(arg))
			}
		}
	}
	return remoteName, refspecs, opts, true
}

func bridgePush(ctx context.Context, repo *git.Repository, args []string) remoteBridgeResult {
	remoteName, displaySpecs, opts, ok := parsePush(args)
	if !ok {
		return remoteBridgeResult{}
	}
	remote, supported, err := bridgedRemote(repo, remoteName, true)
	if err != nil {
		return remoteBridgeResult{handled: true, err: err}
	}
	if !supported {
		return remoteBridgeResult{}
	}
	remoteURL := remote.Config().URLs[len(remote.Config().URLs)-1]
	auth, err := bridgeAuth(ctx, remoteURL)
	if err != nil {
		return remoteBridgeResult{handled: true, err: err}
	}
	opts.RemoteName = remote.Config().Name
	opts.Auth = auth
	var progress bytes.Buffer
	opts.Progress = &progress
	err = remote.PushContext(ctx, opts)
	displayURL := redactRemoteURL(remoteURL)
	if errors.Is(err, git.NoErrAlreadyUpToDate) {
		return remoteBridgeResult{
			handled: true,
			stdout:  fmt.Sprintf("To %s\n=\t%s\t[up to date]\nDone\n", displayURL, strings.Join(displaySpecs, " ")),
			stderr:  progress.String(),
		}
	}
	if err != nil {
		if ref, rejected := nonFastForwardRef(err); rejected {
			spec := ref
			if len(displaySpecs) != 0 {
				spec = displaySpecs[0]
			}
			return remoteBridgeResult{
				handled: true,
				stdout:  fmt.Sprintf("To %s\n!\t%s\t[rejected] (fetch first)\nDone\n", displayURL, spec),
				stderr:  progress.String(),
				err:     err,
			}
		}
		return remoteBridgeResult{handled: true, stderr: progress.String(), err: err}
	}
	return remoteBridgeResult{
		handled: true,
		stdout:  fmt.Sprintf("To %s\nDone\n", displayURL),
		stderr:  progress.String(),
	}
}

func parsePush(args []string) (string, []string, *git.PushOptions, bool) {
	opts := &git.PushOptions{}
	remoteName := "origin"
	seenRemote := false
	var specs []string
	for _, arg := range args {
		switch arg {
		case "--porcelain", "--quiet", "-q", "--verbose", "-v":
			// Formatting/progress switches do not alter the transfer.
		case "--force", "-f":
			opts.Force = true
		case "--prune":
			opts.Prune = true
		default:
			if strings.HasPrefix(arg, "-") {
				return "", nil, nil, false
			}
			if !seenRemote {
				remoteName, seenRemote = arg, true
			} else {
				specs = append(specs, arg)
				opts.RefSpecs = append(opts.RefSpecs, config.RefSpec(arg))
			}
		}
	}
	return remoteName, specs, opts, true
}

func nonFastForwardRef(err error) (string, bool) {
	const prefix = "non-fast-forward update: "
	for current := err; current != nil; current = errors.Unwrap(current) {
		if message := current.Error(); strings.HasPrefix(message, prefix) {
			return strings.TrimPrefix(message, prefix), true
		}
	}
	return "", false
}

func bridgeList(ctx context.Context, repo *git.Repository, args []string) remoteBridgeResult {
	remoteName, patterns, listOpts, ok := parseList(args)
	if !ok {
		return remoteBridgeResult{}
	}
	remote, supported, err := bridgedRemote(repo, remoteName, false)
	if err != nil {
		return remoteBridgeResult{handled: true, err: err}
	}
	if !supported {
		return remoteBridgeResult{}
	}
	auth, err := bridgeAuth(ctx, remote.Config().URLs[0])
	if err != nil {
		return remoteBridgeResult{handled: true, err: err}
	}
	listOpts.Auth = auth
	refs, err := remote.ListContext(ctx, listOpts)
	if err != nil {
		return remoteBridgeResult{handled: true, err: err}
	}
	sort.Slice(refs, func(i, j int) bool { return refs[i].Name() < refs[j].Name() })
	byName := make(map[plumbing.ReferenceName]*plumbing.Reference, len(refs))
	for _, ref := range refs {
		byName[ref.Name()] = ref
	}
	var out strings.Builder
	for _, ref := range refs {
		name := ref.Name().String()
		if !matchRemoteRef(name, patterns) {
			continue
		}
		hash := ref.Hash()
		if ref.Type() == plumbing.SymbolicReference {
			if target := byName[ref.Target()]; target != nil {
				hash = target.Hash()
			}
		}
		if hash.IsZero() {
			continue
		}
		fmt.Fprintf(&out, "%s\t%s\n", hash, name)
	}
	return remoteBridgeResult{handled: true, stdout: out.String()}
}

func parseList(args []string) (string, []string, *git.ListOptions, bool) {
	remoteName := ""
	var patterns []string
	for _, arg := range args {
		switch arg {
		case "--refs":
			// Peeled pseudo-refs are already omitted by IgnorePeeled.
		case "--quiet", "-q":
		case "--heads", "--branches", "-h", "--tags", "-t", "--symref":
			// Not needed by Meads' refs/meads/* probe; retain native behavior
			// for invocations whose filtering/output shape differs.
			return "", nil, nil, false
		default:
			if strings.HasPrefix(arg, "-") {
				return "", nil, nil, false
			}
			if remoteName == "" {
				remoteName = arg
			} else {
				patterns = append(patterns, arg)
			}
		}
	}
	if remoteName == "" {
		return "", nil, nil, false
	}
	return remoteName, patterns, &git.ListOptions{PeelingOption: git.IgnorePeeled}, true
}

func matchRemoteRef(name string, patterns []string) bool {
	if len(patterns) == 0 {
		return true
	}
	for _, pattern := range patterns {
		if pattern == name {
			return true
		}
		// git ls-remote's ref patterns allow '*' to span '/' components;
		// path.Match deliberately does not. Meads uses one trailing wildcard,
		// but accepting a single wildcard anywhere keeps this helper honest.
		if strings.Count(pattern, "*") == 1 {
			prefix, suffix, _ := strings.Cut(pattern, "*")
			if strings.HasPrefix(name, prefix) && strings.HasSuffix(name, suffix) {
				return true
			}
		}
	}
	return false
}

func bridgedRemote(repo *git.Repository, name string, push bool) (*git.Remote, bool, error) {
	remote, err := repo.Remote(name)
	if err != nil {
		return nil, true, err
	}
	urls := remote.Config().URLs
	if len(urls) == 0 {
		return nil, true, fmt.Errorf("remote %q has no URLs", name)
	}
	url := urls[0]
	if push {
		url = urls[len(urls)-1]
	}
	endpoint, err := transport.NewEndpoint(url)
	if err != nil {
		return nil, true, err
	}
	switch strings.ToLower(endpoint.Protocol) {
	case "http", "https", "ssh", "git":
		return remote, true, nil
	default:
		return remote, false, nil
	}
}

// bridgeAuth keeps credentials in the host. HTTP supports credentials in the
// URL, or explicit environment variables for non-interactive use. SSH uses
// go-git's normal ssh-agent + ~/.ssh/config + known_hosts behavior unless an
// explicit private key is selected.
func bridgeAuth(ctx context.Context, remoteURL string) (transport.AuthMethod, error) {
	endpoint, err := transport.NewEndpoint(remoteURL)
	if err != nil {
		return nil, err
	}
	switch strings.ToLower(endpoint.Protocol) {
	case "http", "https":
		username := os.Getenv("MEADS_GIT_HTTP_USERNAME")
		password := os.Getenv("MEADS_GIT_HTTP_PASSWORD")
		if token := os.Getenv("MEADS_GIT_HTTP_TOKEN"); token != "" {
			password = token
			if username == "" {
				username = "git"
			}
		}
		if username != "" || password != "" {
			return &httpauth.BasicAuth{Username: username, Password: password}, nil
		}
	case "ssh":
		username := endpoint.User
		if username == "" {
			username = sshauth.DefaultUsername
		}
		var auth sshauth.AuthMethod
		if key := os.Getenv("MEADS_GIT_SSH_KEY"); key != "" {
			auth, err = sshauth.NewPublicKeysFromFile(username, key, os.Getenv("MEADS_GIT_SSH_PASSPHRASE"))
			if err != nil {
				return nil, fmt.Errorf("load SSH key: %w", err)
			}
		} else if endpoint.Password != "" {
			auth = &sshauth.Password{User: username, Password: endpoint.Password}
		} else if _, hasDeadline := ctx.Deadline(); hasDeadline {
			// go-git's default SSH auth is agent-backed, but its connection is
			// established before the pack session sees ctx. Construct it here
			// when a deadline exists so the dial itself is bounded below.
			auth, err = sshauth.NewSSHAgentAuth(username)
			if err != nil {
				return nil, err
			}
		}
		if auth != nil {
			if deadline, ok := ctx.Deadline(); ok {
				timeout := time.Until(deadline)
				if timeout <= 0 {
					return nil, ctx.Err()
				}
				auth = &deadlineSSHAuth{AuthMethod: auth, timeout: timeout}
			}
			return auth, nil
		}
	}
	return nil, nil
}

type deadlineSSHAuth struct {
	sshauth.AuthMethod
	timeout time.Duration
}

func (a *deadlineSSHAuth) ClientConfig() (*gossh.ClientConfig, error) {
	config, err := a.AuthMethod.ClientConfig()
	if err != nil {
		return nil, err
	}
	config.Timeout = a.timeout
	return config, nil
}

func redactRemoteURL(raw string) string {
	endpoint, err := transport.NewEndpoint(raw)
	if err != nil || (endpoint.User == "" && endpoint.Password == "") {
		return raw
	}
	endpoint.User = ""
	endpoint.Password = ""
	return endpoint.String()
}
