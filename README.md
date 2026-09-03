# Meads on GitHub

This orphan branch is the static GitHub Pages frontend for Meads. It reads and
writes the `refs/meads/*` namespace using GitHub's REST Git Database API; it
does not clone the repository or require a Meads server.

Public repositories open read-only without authentication. Editing requires a
fine-grained personal access token restricted to the selected repository with
**Contents: read and write** permission. The token is kept in memory or
`sessionStorage` for the current browser tab, never in a URL or permanent
storage.

## Development

```sh
python3 -m http.server 4173
npm test
```

The site has no build step and no remotely hosted JavaScript. `marked` and
DOMPurify are vendored under `vendor/`.

## Backend limitations

- GitHub's GraphQL API does not expose custom Meads refs, so this frontend uses
  the REST Git Database endpoints.
- A task update creates a new `task.json` tree and commit parented on the ref's
  previously read tip, then updates the ref with `force: false`. A concurrent
  edit therefore fails the fast-forward check and is retried from fresh state.
- GitHub REST has no atomic multi-ref transaction. Soft delete is allowed only
  when no live task depends on the target; otherwise the frontend refuses it.
- Repositories with Meads `remote_locking` enabled are read-only here because
  REST does not provide the conditional ref deletion required by that protocol.
