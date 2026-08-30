# Embedded wasm-git performance experiment

This branch adds an opt-in Git backend that runs libgit2 from the sibling
[`wasm-git`](https://github.com/petersalomonsen/wasm-git) source inside
[wazero](https://wazero.io/). The module uses WASI Preview 1 file access and is
mounted read/write on the repository's Git directory only. It cannot see the
working tree or the process environment.

Enable it for a Meads command with:

```sh
MEADS_GIT_RUNTIME=wasm md list
```

The embedded module handles the local plumbing hot path (`hash-object`,
`mktree`, `commit-tree`, `for-each-ref`, `cat-file`, `update-ref`, and
`rev-list`). Repository discovery and network/config commands fall back to the
native Git executable. Multi-ref `update-ref --stdin` also remains native,
because libgit2's transaction API locks all refs but does not promise rollback
after an unexpected mid-commit I/O failure. This keeps Meads' all-or-none batch
guarantee, leaves fetch/push behavior unchanged, and limits the experiment to
safe operations Meads stores in `refs/meads/*`.

## Results

Measured 2026-08-30 on Linux arm64 with Go 1.25.6, wazero 1.12.0, libgit2
1.9.4, and warm wazero compilation cache. Values are medians of three 1-second
Go benchmark runs:

| Operation | Native Git CLI | wazero + WASI + libgit2 | Result |
| --- | ---: | ---: | --- |
| Commit one task ref (blob + tree + commit + CAS ref) | 26.59 ms | 15.64 ms | WASM 1.70× faster |
| Load 100 tasks (ref scan + batched blob read) | 10.31 ms | 17.50 ms | WASM 1.70× slower |
| Construct backend + list one ref | 1.484 ms | 24.34 ms | WASM 16.4× slower |

Run the comparison with:

```sh
go test ./pkg/meads -run '^$' -bench '^BenchmarkGitBackend' -benchtime=1s -count=3
```

The allocation result is the clearest prototype limitation. Each WASI command
uses a fresh module instance; one four-command ref mutation allocates roughly
152 MB in aggregate, versus 78 KB in the Go parent for native Git. The native
subprocess has memory outside Go's allocation accounting, so those byte figures
are not an RSS comparison, but the WASM churn is still real. Reusing a reactor-
style module instance and passing requests through linear memory is the next
experiment if mutation throughput warrants it.

The first-ever command also pays compilation cost. Wazero's compiled-code cache
is persisted under the user cache directory (`$XDG_CACHE_HOME/meads/wazero`, or
the platform equivalent); set `MEADS_WAZERO_CACHE` to override it. The startup
number above is the more representative repeat invocation after that cache is
warm.

## Build and architecture

The generated 589 KB `pkg/meads/wasmgit.wasm` is embedded in the Go binary.
See [`experimental/wasmgit/README.md`](../experimental/wasmgit/README.md) for
the reproducible Zig/WASI build. The build uses wasm-git's patched libgit2
1.9.4 source, extends its wasm32 integer fix to WASI, disables sockets and
process spawning, and links only local Git functionality.

`WazeroGit` implements the existing `meads.Git` interface. It caches compiled
WebAssembly code and instantiates an anonymous WASI command per call, allowing
concurrent callers while preserving libgit2's filesystem ref locks and CAS
transactions. Linked worktrees mount their common Git directory and open the
worktree administration directory below it, so every worktree still shares the
same `refs/meads/*` task space. Single-ref compare-and-swap mutations remain
atomic and are covered by a concurrent race test.
