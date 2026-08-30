# Embedded wasigit experiment

Meads embeds the upstream C Git WASI module built by the sibling
[`wasigit`](https://github.com/jpillora/wasigit) repository. The module runs in
wazero and sees only the repository's Git administration directory through a
read/write WASI filesystem mount. The module itself remains networkless; Meads
handles its `ls-remote`, fetch, and push flows with a Go host transport bridge
for HTTP, HTTPS, SSH, and `git://`. Unsupported command shapes and local/file
remotes remain native Git fallbacks.

Build both the source module and embedded copy from this Meads worktree:

```sh
experimental/wasigit/build.sh
```

Set `WASIGIT_SOURCE` if the source checkout is not at `../wasigit`. The script
runs wasigit's checksum-pinned Git 2.55.0/WASI SDK 34 build and copies its
release-stripped output to `pkg/meads/wasigit.wasm` for `go:embed`.

The embedded module contains upstream Git and is GPL-2.0-only. See
`COPYING.git`.
