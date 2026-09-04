# meads.jpillora.com

This branch contains both the static GitHub Pages frontend and the globally
accessible Cloudflare Worker at `meads.jpillora.com`. The Worker proxies public
assets from `https://jpillora.github.io/meads/` and owns only the GitHub App
OAuth exchange/session endpoints under `/_/github/*`; repository and Meads ref
traffic remains browser-to-GitHub.

## Files

- `src/config.js` — checked-in public origin, GitHub App, endpoint, and cookie settings
- `src/worker.js` — asset proxy and stateless encrypted-cookie OAuth broker
- `test/worker.test.js` — broker/proxy integration tests with mocked GitHub calls
- `index.html`, `app.js`, `github.js` — no-build browser frontend
- `meads.wasm`, `wasm.js`, `wasm_exec.js` — browser Meads Go core

## Security

`GITHUB_CLIENT_SECRET` is the sole Worker secret. Never print or commit it. The
Worker derives separate pending/session AES-GCM keys with HKDF; there is no
session database or second secret. OAuth access tokens are returned to the
same-origin frontend and held only in memory. PAT fallback remains tab-scoped.

The site is intentionally global, not AU-gated. Keep every `/_/github/*` route
ahead of the asset fallback and uncached.

## GitHub App

The checked-in public App identity is `meads-tasks` (owner `jpillora`, App ID
`4827121`, client ID `Iv23liw7OyyeQFu2bI9t`). Its homepage is the public origin,
the callback is `/_/github/callback`, and its setup URL is `/`. It requests only
repository Metadata read and Contents read/write, has no webhook or subscribed
events, uses expiring user tokens, and permits installation by any account.

## Verify and deploy

```sh
npm test
bunx wrangler deploy
```

The repo-root jpilloracom `dev.ts` also discovers this checkout as
`sites/meads` after it is added as a submodule.
