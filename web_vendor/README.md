# Vendored front-end dependencies (no build step)

The web UI (`pkg/webui/assets`) is deliberately **no-build**: no npm, no bundler,
no runtime CDN. Third-party JavaScript is brought in as plain ESM instead.

## How it works

- These files are committed here and embedded into the `md` binary by
  `//go:embed assets` (`pkg/webui/assets.go`); the server serves them with a
  plain `http.FileServer`, so each is reachable at its path, e.g.
  `web_vendor/marked.esm.js` → `GET /web_vendor/marked.esm.js`.
- `index.html` declares an [import map](https://developer.mozilla.org/docs/Web/HTML/Element/script/type/importmap)
  mapping bare specifiers to those paths, so `app.js` (a `<script type="module">`)
  can `import { marked } from "marked"` with no bundler.

## Selection rule

Only vendor libraries that ship a **single, self-contained ESM file** with zero
or few runtime deps. Libraries with deep dependency graphs (TipTap, ProseMirror,
Lexxy, …) cannot be hand-vendored without a bundler and are therefore out of
scope — which is why a true WYSIWYG editor is not pursued for this UI.

## Inventory

| Specifier   | File              | Version        | Source                                                          | sha256 |
|-------------|-------------------|----------------|-----------------------------------------------------------------|--------|
| `marked`    | `marked.esm.js`   | marked 17.0.4  | https://cdn.jsdelivr.net/npm/marked@17.0.4/lib/marked.esm.js     | `58f9db265e94c44298c0b85eb6a9b1c6a97cdac3801c4f89380fddb6c4531615` |
| `dompurify` | `purify.es.mjs`   | DOMPurify 3.4.11 | https://cdn.jsdelivr.net/npm/dompurify@3.4.11/dist/purify.es.mjs | `8a40d0a0f66c217879826a4e97bca5ef88f1b751fe813d27cf4195165aa3778f` |

`marked` + `dompurify` are vendored for the description markdown rendering work
(see TASKS #47): `marked` parses, `dompurify` sanitises the resulting HTML.

## Refreshing / bumping a version

From this directory, re-download and re-verify, then bump the table above:

```sh
curl -sL https://cdn.jsdelivr.net/npm/marked@17.0.4/lib/marked.esm.js   -o marked.esm.js
curl -sL https://cdn.jsdelivr.net/npm/dompurify@3.4.11/dist/purify.es.mjs -o purify.es.mjs
sha256sum marked.esm.js purify.es.mjs
```

After changing a filename or specifier, update the import map in `../index.html`
to match. Re-`go install ./cmd/md` so the embedded copy is refreshed.
