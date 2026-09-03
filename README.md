# Meads on GitHub

This orphan branch is the static GitHub Pages frontend for Meads. It reads and
writes the `refs/meads/*` namespace using GitHub's REST Git Database API; it
does not clone the repository or require a Meads server.

Task mutations run through the pinned Meads Go package in browser WebAssembly.
Go owns task defaults, normalization, timestamps, dependency validation and
cycle detection. JavaScript owns GitHub transport and fast-forward ref updates,
so the GitHub token is never passed into WASM.

The GitHub refs adapter persists public snapshots, ref ETags and immutable
`(commit SHA, filename)` objects in browser local storage. A repeat visit paints
the snapshot immediately, then conditionally checks the ref namespace; an
unchanged namespace is a `304`, while a changed one downloads only objects at
previously unseen SHAs. Private-repository task data is never persisted there.

Public repositories open read-only without authentication. Editing requires a
fine-grained personal access token restricted to the selected repository with
**Contents: read and write** permission. The token is kept in memory or
`sessionStorage` for the current browser tab, never in a URL or permanent
storage.

A true **Sign in with GitHub** flow cannot be implemented safely by GitHub
Pages alone. GitHub's authorization-code exchange requires a client secret and
its token endpoints are not a browser-CORS authentication API. Adding that UX
requires a small OAuth token broker (for example, a serverless endpoint); no
client secret should ever be embedded in this site.

## Development

```sh
python3 -m http.server 4173
npm run build:wasm
npm test
```

The checked-in site has no runtime build step and no remotely hosted JavaScript.
`npm run build:wasm` rebuilds `meads.wasm` and copies the matching Go runtime.
The pinned Meads source is under `third_party/meads`; its one browser-only lock
shim exists because upstream currently defines that unused platform seam only
for Unix and Windows. `marked` and DOMPurify are under `web_vendor/`.

The long-term direction is to share this frontend with `md webui`. UI code
should therefore depend on Meads-shaped operations, while the host adapter owns
transport: local HTTP for `md webui`, or GitHub refs plus OAuth/PAT for Pages.

## Backend limitations

- GitHub's GraphQL API does not expose custom Meads refs, so this frontend uses
  the REST Git Database endpoints.
- GitHub does not provide a streaming custom-ref feed. Delta sync therefore
  conditionally checks the small ref index and content-addresses every task
  object by commit SHA.
- A task update creates a new `task.json` tree and commit parented on the ref's
  previously read tip, then updates the ref with `force: false`. A concurrent
  edit therefore fails the fast-forward check and is retried from fresh state.
- GitHub REST has no atomic multi-ref transaction. Soft delete is allowed only
  when no live task depends on the target; otherwise the frontend refuses it.
- Repositories with Meads `remote_locking` enabled are read-only here because
  REST does not provide the conditional ref deletion required by that protocol.
