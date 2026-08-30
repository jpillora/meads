# Embedded upstream Git WASI performance experiment

This branch adds an opt-in Git backend that runs the upstream C Git executable
compiled by [`wasigit`](https://github.com/jpillora/wasigit) inside
[wazero](https://wazero.io/). The module uses WASI Preview 1 file access and is
mounted read/write on the repository's Git administration directory only. It
cannot see the working tree or the host process environment.

Enable it for a Meads command with:

```sh
MEADS_GIT_RUNTIME=wasm md list
```

The embedded module handles the local plumbing hot path (`hash-object`,
`mktree`, `commit-tree`, `for-each-ref`, `cat-file`, `update-ref`, and
`rev-list`). This includes transactional multi-ref `update-ref --stdin`, using
upstream Git's normal lock/prepare/commit implementation. A Go host transport
bridge handles the remote operations Meads uses (`ls-remote`, `fetch`, and
`push`) over HTTP, HTTPS, SSH, and the unauthenticated `git://` protocol. It
performs negotiation, pack generation/indexing, and network I/O without starting
Git's remote helpers or pack subprocesses.

The WASM module itself still has no sockets or process creation. The bridge is
implemented with [go-git](https://github.com/go-git/go-git), operating directly
on the same Git object database and refs that are mounted into WASM. Local and
`file://` remotes, unsupported command options, and Git commands outside the
experiment's deliberately small surface retain the native Git fallback.

### Remote authentication

HTTP Basic authentication can come from credentials in the remote URL or from:

```text
MEADS_GIT_HTTP_USERNAME
MEADS_GIT_HTTP_PASSWORD
MEADS_GIT_HTTP_TOKEN
```

`MEADS_GIT_HTTP_TOKEN` is used as the password and defaults the username to
`git`. SSH uses the agent selected by `SSH_AUTH_SOCK`, host/port aliases from
the user's SSH config, and normal `known_hosts` verification. Set
`SSH_KNOWN_HOSTS` to select another known-hosts file. A private key can be
selected explicitly with:

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

| Operation | Native Git CLI | wazero + upstream C Git | Result |
| --- | ---: | ---: | --- |
| Commit one task ref (blob + tree + commit + CAS ref) | 46.45 ms | 16.96 ms | WASM 2.74× faster |
| Load 100 tasks (ref scan + batched blob read) | 14.12 ms | 22.37 ms | WASM 1.58× slower |
| Construct backend + list one ref | 3.008 ms | 59.19 ms | WASM 19.7× slower |

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
allocates about 78.9 MB in aggregate, down from roughly 152 MB with the libgit2
module, versus about 79 KB in the Go parent for native Git. Native subprocess
memory is outside Go's allocation accounting, so those figures are not an RSS
comparison. A reactor-style module would be the next experiment for reducing
WASM instance churn.

The first-ever command also pays compilation cost. Wazero's compiled-code cache
is persisted under the user cache directory (`$XDG_CACHE_HOME/meads/wazero`, or
the platform equivalent); set `MEADS_WAZERO_CACHE` to override it. The startup
number above represents repeat invocations with that cache warm.

## Size results

Release builds use `go build -trimpath -ldflags='-s -w'`; gzip values use
`gzip -9`:

| Build | Raw | gzip -9 |
| --- | ---: | ---: |
| Native Meads at `main` | 10.75 MiB | 4.04 MiB |
| Previous Meads + libgit2 WASI | 13.88 MiB | 5.09 MiB |
| Meads + upstream C Git WASI, local only | 14.94 MiB | 5.47 MiB |
| Meads + upstream C Git WASI + host bridge | 17.88 MiB | 6.63 MiB |

The new embedded module is 1,663,836 bytes raw and 616,179 bytes compressed.
It is unchanged by the network work. The bridge and its Go dependencies add
3,080,192 bytes raw / 1,217,285 bytes compressed to the local-only upstream Git
build. The complete bridged executable is 18,743,561 bytes raw and 6,956,901
bytes compressed, adding 7,471,104 bytes raw / 2,722,391 bytes compressed over
native Meads.

## Build and architecture

See [`experimental/wasigit/README.md`](../experimental/wasigit/README.md) for
the embedding build. The sibling wasigit repository pins Git 2.55.0, zlib
1.3.1, and WASI SDK 34, and keeps its compatibility patch reviewable. Network,
subprocess, hook, pager, and shell-helper functionality remains omitted from the
module because WASI Preview 1 does not provide Git's process and socket model;
the host bridge supplies the remote layer instead.

`WazeroGit` implements the existing `meads.Git` interface. It caches compiled
WebAssembly code and instantiates an anonymous WASI command per call, allowing
concurrent callers while preserving upstream Git's filesystem locks. Linked
worktrees mount their common Git directory and set the worktree administration
directory through `GIT_DIR`, so every worktree shares the same `refs/meads/*`
task space. Tests cover concurrent single-ref CAS and successful plus rejected
multi-ref transactions, including all-or-none rollback on a stale ref. Separate
integration tests run smart HTTP and SSH servers and verify push, `ls-remote`,
fetch, object readability through WASM, and non-fast-forward push rejection.
