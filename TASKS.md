# TASKS

a [meads](https://github.com/jpillora/meads) (`md`) managed task log

* created: 2026-02-14T11:42:09Z
* updated: 2026-06-06T01:45:07Z
* next-id: 13

## 20. VS Code extension end-to-end manual test

* status: inprogress
* priority: P2
* type: feature
* depends-on: 
* created: 2026-05-21T07:29:29Z
* updated: 2026-05-21T16:36:32Z

Package the extension locally (vsce package in vscode/), install the .vsix in **desktop VS Code** (NOT 'code serve-web' / vscode.dev / Codespaces — those are blocked by mixed-content; see #26), open a TASKS.md, confirm webui renders inside the webview, confirm changes round-trip, confirm bind-vscode JSON-RPC works (vscode.openFile, vscode.showQuickPick, vscode.copyToClipboard, vscode.openExternal, vscode.showMessage), confirm subprocess cleanup on tab close. Note: bearer token currently rides in the iframe query string — fine inside a sandboxed webview but worth a glance during review.

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
* depends-on: 20,21
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

## 26. extension doesn't work in code serve-web / vscode.dev / Codespaces

* status: open
* priority: P3
* type: bug
* created: 2026-05-21T16:36:32Z

The webview iframe loads md webui over HTTP, but VS Code's web client hosts webviews on an HTTPS origin (*.vscode-cdn.net). Chrome blocks the iframe as mixed-content before VS Code's portMapping/asExternalUri proxy can intercept (verified empirically with code serve-web 1.103.2 + agent-browser: no request reaches md webui's HTTP handler). Fix options: (a) serve md webui over HTTPS with a self-signed cert; (b) refactor the webview to load static assets via webview.asWebviewUri and proxy all API/SSE/WS traffic through the existing /bind-vscode channel; (c) document desktop-only and stop pretending. Track this before promoting the extension beyond desktop.

## 30. Git mode

* status: draft
* priority: P2
* type: idea
* created: 2026-06-06T01:24:43Z

Git mode

The tasks file is moved into JSON and out of the working tree, and it’ll live purely in gitrefs 

When you init in git mode, a meads ref tree is made, with an empty tasks json as the first “virtual commit”

All changes are persisted solely in git

## 31. Add 'md auto-save' command

* status: closed
* priority: P2
* type: feature
* created: 2026-06-06T01:34:29Z
* updated: 2026-06-06T01:45:07Z

Pre-commit hook (sibling to auto-delete) that stages the tasks file so it rides along in every commit. Extract a shared hookBlock helper from auto_delete.go, add auto_save.go, wire into main.go, add e2e coverage. Marker-based enablement, no git config. No default-branch guard, no backup/restore (never modifies content).
