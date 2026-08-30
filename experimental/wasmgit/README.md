# Embedded wasm-git experiment

This directory builds the local Git plumbing used by Meads as a WASI Preview 1
command. The module links libgit2 from the sibling `wasm-git` checkout, runs in
wazero, and sees only the repository's Git directory through a read/write WASI
filesystem mount. Network commands are intentionally left to native Git.

Build from the Meads worktree after preparing the sibling checkout:

```sh
../wasm-git/setup.sh # from the wasm-git checkout, once
experimental/wasmgit/build.sh
```

Set `WASM_GIT_SOURCE` when the source checkout is elsewhere. The generated
`pkg/meads/wasmgit.wasm` is embedded into the Go binary so the runtime does not
need an external executable or shared library.

`libgit2-wasi.patch` extends wasm-git's wasm32 integer fix to Zig's WASI libc.
It is applied only to the downloaded, gitignored `libgit2` build source.

The embedded library remains covered by libgit2's GPLv2 license with linking
exception; see `COPYING.libgit2`.
