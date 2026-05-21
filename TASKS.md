# TASKS

a [meads](https://github.com/jpillora/meads) (`md`) managed task log

* created: 2026-02-14T11:42:09Z
* updated: 2026-05-21T07:56:47Z
* next-id: 13

## 20. VS Code extension end-to-end manual test

* status: open
* priority: P2
* type: feature
* depends-on: 
* created: 2026-05-21T07:29:29Z
* updated: 2026-05-21T07:53:42Z

Package the extension locally (vsce package in vscode/), install the .vsix in VS Code, open a TASKS.md, confirm webui renders inside the webview, confirm changes round-trip, confirm bind-vscode JSON-RPC works (vscode.openFile, vscode.showQuickPick, vscode.copyToClipboard, vscode.openExternal, vscode.showMessage), confirm subprocess cleanup on tab close. Also test in a Remote-SSH or Codespaces workspace once #24 lands. Note: bearer token currently rides in the iframe query string — fine inside a sandboxed webview but worth a glance during review.

## 21. meads.minMdVersion setting is dead code

* status: open
* priority: P3
* type: bug
* depends-on: 20
* created: 2026-05-21T07:29:29Z
* updated: 2026-05-21T07:29:34Z

The setting is declared in vscode/package.json and getVersion() exists in vscode/src/mdBinary.ts, but nothing ever calls getVersion or compares against minMdVersion. Either wire it up (warn on mismatch in resolveMd) or remove both the setting and getVersion().

## 22. Cut first VS Code extension release (v1)

* status: open
* priority: P2
* type: task
* depends-on: 20,21,25
* created: 2026-05-21T07:29:29Z
* updated: 2026-05-21T07:53:42Z

After the e2e test passes, push a v* tag — the release_vscode job in .github/workflows/ci.yml will package meads-vX.Y.Z.vsix and attach it to the GitHub release.

## 23. Restore VS Code extension section to README

* status: open
* priority: P3
* type: task
* depends-on: 22
* created: 2026-05-21T07:29:29Z
* updated: 2026-05-21T07:29:34Z

Once the .vsix is attached to a real release, re-add the 'Web UI + VS Code extension' section to README.md with install instructions (download .vsix from Releases, install via 'Extensions: Install from VSIX...').

## 25. Set explicit Content-Security-Policy on Meads webview

* status: open
* priority: P3
* type: task
* created: 2026-05-21T07:53:31Z

vscode/src/webview.ts emits no CSP meta tag, so it relies on VS Code's default permissive CSP for webviews with scripts enabled. A future VS Code release could tighten the default and break the iframe. Add a <meta http-equiv="Content-Security-Policy"> tag whitelisting frame-src for the iframe origin (and connect-src for the websocket). The origin will be dynamic — derive it from info.url (post-asExternalUri).
