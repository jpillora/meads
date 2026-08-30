# Tigo embedded Git performance experiment

This branch adds an opt-in Git backend through
[`tigo`](https://github.com/jpillora/tigo), an importable Go package that runs
the upstream C Git executable inside [wazero](https://wazero.io/). Meads opens
tigo in its Git-directory-only mode: the WASI module receives read/write access
to the repository's Git administration directory, but cannot see the worktree
or host process environment.

Enable it for a Meads command with:

```sh
MEADS_GIT_RUNTIME=wasm md list
```

Tigo's embedded module handles the local plumbing hot path (`hash-object`,
`mktree`, `commit-tree`, `for-each-ref`, `cat-file`, `update-ref`, and
`rev-list`). This includes transactional multi-ref `update-ref --stdin`, using
upstream Git's normal lock/prepare/commit implementation. A Go host transport
bridge handles the remote operations Meads uses (`ls-remote`, `fetch`, and
`push`) over HTTP, HTTPS, SSH, and the unauthenticated `git://` protocol. It
performs authentication, packet-line framing, and network I/O without starting
Git's remote helpers or pack subprocesses; embedded Git's `pack-objects` and
`index-pack` builtins own pack creation and ingestion.

The WASM module itself still has no sockets or process creation. Tigo's narrow
Go bridge supplies authenticated HTTP, SSH, and `git://` byte streams plus Git
packet-line framing. Embedded C Git generates and indexes the transferred
packs, selects objects, checks out worktrees, and updates refs; tigo does not
embed a second Git implementation. Tigo explicitly reports unsupported
commands and transports; the Meads adapter then retains its native Git fallback
for local and `file://` remotes and for commands outside the experiment's
deliberately small delegation surface.

### Remote authentication

HTTP Basic authentication can come from credentials in the remote URL or from:

```text
MEADS_GIT_HTTP_USERNAME
MEADS_GIT_HTTP_PASSWORD
MEADS_GIT_HTTP_TOKEN
```

`MEADS_GIT_HTTP_TOKEN` is used as the password and defaults the username to
`git`. SSH uses the agent selected by `SSH_AUTH_SOCK` and normal `known_hosts`
verification. Set `SSH_KNOWN_HOSTS` to select another known-hosts file. A
private key can be selected explicitly with:

```text
MEADS_GIT_SSH_KEY
MEADS_GIT_SSH_PASSPHRASE
```

The bridge is non-interactive: it does not prompt for credentials, passwords,
new host-key approval, or key passphrases.

## Performance results

Measured again after adding the host bridge on 2026-08-30, on Linux arm64 with
Go 1.25.6, wazero 1.12.0, upstream Git 2.55.0, and a warm wazero compilation
cache. Values are medians of three 1-second Go benchmark runs:

| Operation | Native Git CLI | tigo | Result |
| --- | ---: | ---: | --- |
| Commit one task ref (blob + tree + commit + CAS ref) | 31.95 ms | 12.45 ms | tigo 2.57× faster |
| Load 100 tasks (ref scan + batched blob read) | 11.08 ms | 15.38 ms | tigo 1.39× slower |
| Construct backend + list one ref | 1.811 ms | 60.91 ms | tigo 33.6× slower |

The host bridge is not on any of these local operation paths; the wider spread
from the earlier run reflects shared-host benchmark variance rather than remote
transport work. For the matched pre-bridge run used to compare the two WASI
implementations directly, upstream C Git was 11% faster for a task-ref mutation
and 21% faster for loading 100 tasks, but 2.17 times slower for short-lived
startup/list:

| Operation | libgit2 WASI | upstream C Git WASI |
| --- | ---: | ---: |
| Commit one task ref | 15.64 ms | 13.94 ms |
| Load 100 tasks | 17.50 ms | 13.88 ms |
| Construct backend + list one ref | 24.34 ms | 52.82 ms |

Run the comparison with:

```sh
go test ./pkg/meads -run '^$' -bench '^BenchmarkGitBackend' -benchtime=1s -count=3
```

Each command still gets a fresh module instance. One four-command ref mutation
allocates about 80.1 MB in aggregate, down from roughly 152 MB with the libgit2
module, versus about 79 KB in the Go parent for native Git. Native subprocess
memory is outside Go's allocation accounting, so those figures are not an RSS
comparison. The pack plumbing increases the module's per-instance static
memory; a reactor-style module would be the next experiment for reducing WASM
instance churn.

The first-ever command also pays compilation cost. Tigo's wazero compiled-code
cache is persisted under the user cache directory (`$XDG_CACHE_HOME/tigo/wazero`,
or the platform equivalent); set `MEADS_WAZERO_CACHE` to override it. The
startup number above represents repeat invocations with that cache warm.

## Size results

Release builds use `go build -trimpath -ldflags='-s -w'`; gzip values use
`gzip -9`:

| Build | Raw | gzip -9 |
| --- | ---: | ---: |
| Native Meads at `main` | 10.75 MiB | 4.04 MiB |
| Previous Meads + libgit2 WASI | 13.88 MiB | 5.09 MiB |
| Meads + upstream C Git WASI, local only | 14.94 MiB | 5.47 MiB |
| Meads + single-engine tigo | 15.69 MiB | 5.79 MiB |
| Standalone tigo diagnostic CLI | 10.25 MiB | 3.90 MiB |

Tigo's embedded module is 1,822,571 bytes raw and 679,215 bytes compressed,
including C Git's `index-pack` and `pack-objects`. The complete Meads executable
is 16,449,801 bytes raw and 6,069,343 bytes compressed, adding 5,177,344 bytes
raw / 1,834,844 bytes compressed over native Meads. Removing go-git saves
2,293,760 bytes raw / 898,920 bytes compressed compared with the previous host
bridge build.

## Build and architecture

The [tigo repository](https://github.com/jpillora/tigo) owns the Go API, checked
in module, reproducible build, compatibility patch, transport bridge, and its
tests. It pins Git 2.55.0, zlib 1.3.1, and WASI SDK 34. Network, subprocess,
hook, pager, and shell-helper functionality remains omitted from the module
because WASI Preview 1 does not provide Git's process and socket model; tigo's
host bridge supplies only authenticated byte transport and protocol framing.

Meads' compatibility-named `WazeroGit` is now a small adapter from tigo's
`Repo`/`Cmd` API to the existing `meads.Git` interface. Tigo instantiates an
anonymous WASI command per call, allowing concurrent callers while preserving
upstream Git's filesystem locks. Linked worktrees mount their common Git
directory and select the worktree administration directory through `GIT_DIR`,
so every worktree shares the same `refs/meads/*` task space. Tigo's tests cover
local object/ref parity, linked worktrees, authenticated smart HTTP and SSH,
push, `ls-remote`, fetch, object readability through WASM, and non-fast-forward
push rejection. An opt-in Docker test repeats clone, fetch, and push against a
real Gitea server. Meads retains its task-level CAS and transaction tests.
