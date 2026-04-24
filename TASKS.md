# TASKS

a [meads](https://github.com/jpillora/meads) (`md`) managed task log

* created: 2026-02-14T11:42:09Z
* updated: 2026-04-24T07:48:07Z
* next-id: 13

## 1. A meads VS code extension

* status: closed
* priority: P2
* type: idea
* created: 2026-04-24T02:25:38Z
* updated: 2026-04-24T07:47:28Z
* deleted: true

A meads VS code extension.

for v1, it should just result in a vsix in GitHub releases, we don't need to be officially published yet.

the extension should do 1 thing:

- when you open a TASKS.md file, the default renderer should be the meads extension
- this rendering is special, it should ensure md is installed, and invoke `md webui` 
- the `md webui` command will host a web server, severing a web UI to edit 1 TASKS.md/csv file
- the meads extension should present this localhost UI
- this UI should mirrors the file contents
  - `<meads file info>` : issue count, last updated
  - `<meads issue list>`  where each issue is rendered
    - `title`
    - `metadata`
    - `body` 
  - and there are buttons to mutate it, and these actions translate into API calls against the `webui` api routes
- the extension may need to surface VS Code functionality, it should do this by also connecting to the `webui` api routes, but this one is a basic JSON RPC endpoint `GET /bind-vscode` and the client (vscode) offers a set of actions to the `webui`
