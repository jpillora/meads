Release a new version. Follow these steps exactly:

## 1. Run all tests

Run `go test ./...` and confirm every package passes. If any test fails, stop and report the failure. Do not proceed.

## 2. Commit outstanding changes

Check `git status` and `git diff` for any uncommitted changes (staged or unstaged).
- If there are changes, stage them and commit with a message consisting of concise dot-point summaries of each change (one `- ` bullet per logical change). Keep each bullet under 80 chars.
- If there are no changes, skip this step.

## 3. Find the latest version tag

Run `git tag --sort=-v:refname | head -1` to find the latest semver tag (format `vMAJOR.MINOR.PATCH`).

## 4. Determine version bump

Look at all commits since the latest tag using `git log <latest-tag>..HEAD --oneline`.

Apply these rules to decide the bump:
- **Minor** bump (e.g. v0.7.1 → v0.8.0): any commit adds a new feature, new command, new flag, new API, or contains a breaking change.
- **Patch** bump (e.g. v0.7.1 → v0.7.2): all commits are bug fixes, refactors, docs, tests, or other non-feature changes.

## 5. Tag and push

- Create an annotated tag: `git tag -a <new-version> -m "<new-version>"`
- Push the commit and tag: `git push && git push --tags`
- Report the new version tag to the user.
