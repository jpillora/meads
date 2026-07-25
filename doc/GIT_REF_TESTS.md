# Git Ref Backend — Host Compatibility Tests

Empirical results for storing meads tasks as **git refs** (`refs/meads/tasks/<id>`)
pointing directly at JSON blobs, instead of (or alongside) a single `TASKS.md`.

Tested **2026-07-25** with git 2.43.0 on linux/aarch64.

## Why test at all

The common warning about custom ref namespaces is:

> Many hosting providers restrict which namespaces you're allowed to push to, so
> test against your actual remote before designing around it.

That warning is real but host-specific, and the failure mode is late and
expensive — you discover it after the storage layer is built. So the namespace
was probed against real remotes before any design work.

## What a ref backend needs

Four independent properties. All four must hold, or the design changes:

1. **Custom namespace accepted** — the host lets you push `refs/meads/*` at all,
   rather than restricting pushes to `refs/heads/*` and `refs/tags/*`.
2. **Refs may point at blobs** — a ref can target a blob directly, with no
   wrapper commit or tree. Without this, every task write costs a commit object.
3. **Advertised and fetchable** — the ref appears in `git ls-remote` and can be
   fetched back, so other clients can actually read tasks.
4. **Server-enforced compare-and-swap** — the *server* verifies the expected old
   value before moving a ref. This is what makes optimistic locking real rather
   than advisory.

## Results

| Property | GitHub | Gitea 1.25.5 |
| --- | :---: | :---: |
| 1. `refs/meads/*` push accepted | ✅ | ✅ |
| 2. Ref → blob directly (no wrapper commit) | ✅ | ✅ |
| 3. Advertised in `ls-remote`, fetchable | ✅ | ✅ |
| 4. Server-enforced compare-and-swap | ✅ | ✅ |

Hosts tested:

- **GitHub** — `git@github.com:jpillora/meads.git`, over SSH.
- **Gitea 1.25.5** — `https://git.oj.jpillora.com/jp/ref-test.git`, self-hosted
  behind Caddy, HTTPS with `~/.netrc` basic auth.

All test refs were deleted from both remotes afterwards.

## Methodology

### Properties 1–3 are straightforward

```bash
# 1 + 2: create a blob, point a custom ref at it, push
BLOB=$(printf '{"hello":"world"}' | git hash-object -w --stdin)
git update-ref refs/meads/tasks/42 "$BLOB"
git push origin refs/meads/tasks/42:refs/meads/tasks/42

# 3: advertised?
git ls-remote origin 'refs/meads/*'

# 3: round-trips into a fresh clone?
git clone "$REMOTE" /tmp/probe
git -C /tmp/probe fetch origin 'refs/meads/*:refs/meads/*'
git -C /tmp/probe cat-file -p refs/meads/tasks/42   # -> {"hello":"world"}
```

Both hosts returned the JSON verbatim, and `cat-file -t` confirmed the ref target
is a `blob`.

### Property 4 has a trap

The obvious test is `git push --force-with-lease`, and on both hosts a stale
lease is rejected:

```
! [rejected]  refs/meads/tasks/42 -> refs/meads/tasks/42 (stale info)
```

**This proves nothing about the server.** Git's lease check runs *client-side*:
`send-pack` compares your lease against the value the server just advertised, and
sets `REF_STATUS_REJECT_STALE` locally. A host that performed no CAS at all would
produce byte-identical output. The check also cannot be defeated by staleness in
your remote-tracking refs, because the comparison uses the fresh advertisement —
so even a deliberate two-client race is inconclusive.

To test the *server*, you must send an old-oid that the git CLI will never send:
a deliberately false one. That means speaking the smart-HTTP protocol directly.

The request body is one pkt-line update command, a flush, and an empty packfile
(empty because the target object already exists server-side):

```
<pkt-len><old-oid> <new-oid> <refname>\0report-status\n
0000
PACK\x00\x00\x00\x02\x00\x00\x00\x00<sha1-of-those-12-bytes>
```

POSTed to `<repo-url>/git-receive-pack` with
`Content-Type: application/x-git-receive-pack-request`.

### The controlled experiment

Two **byte-identical** POSTs were sent, differing only in the claimed old-oid.
The ref's true value was `B`:

| Claimed old-oid | Server response | Ref after |
| --- | --- | --- |
| `A` (false) | `ng refs/meads/tasks/42 …` | unchanged at `B` |
| `B` (true) | `ok refs/meads/tasks/42` | moved to `C` |

Since nothing else varied between the two requests, the CAS check is
unambiguously the cause of the rejection. Both hosts enforce it server-side.

GitHub's rejection names the conflict explicitly:

```
ng refs/meads/tasks/42 cannot lock ref 'refs/meads/tasks/42':
   is at 6100b9a… but expected 09d7cfc…
```

Gitea's does not:

```
ng refs/meads/tasks/42 failed to update ref
```

## Design implications

**The ref backend is viable on both hosts.** `refs/meads/tasks/<id>` holding a
JSON blob, written with CAS pushes, gets genuine server-side lost-update
protection — two agents racing on the same task cannot silently clobber each
other, and the guarantee does not depend on clients behaving well.

Three constraints fall out of the results:

- **Never parse rejection text to detect a conflict.** Gitea's
  `failed to update ref` is generic — it cannot distinguish a CAS conflict from a
  permission error or a rejecting hook. Key the retry loop on the per-ref `ng` /
  rejected *status*, then re-read the current value to decide whether to retry or
  surface the error. Matching on GitHub's much friendlier `cannot lock ref` string
  would silently fail to retry on Gitea.
- **Task refs need an explicit fetch refspec.** A default `git clone` does not
  fetch `refs/meads/*` on either host. Clients must run:
  ```bash
  git fetch origin 'refs/meads/*:refs/meads/*'
  ```
  Consider writing this into `remote.origin.fetch` at init time so tasks sync
  with an ordinary `git fetch`.
- **A repo holding only task refs looks empty.** With no `refs/heads/*`, both
  `git clone` and the Gitea UI report "empty repository" even though the refs
  exist and `ls-remote` lists them. Harmless, but confusing if meads ever uses a
  dedicated tasks-only repo.

See also the `furgit` evaluation for the client-side CAS implementation — its ref
transaction is MT-unsafe under concurrency, so go-git remains the choice for the
CAS path.

## Reproducing on another host

The four properties are host-specific. Before trusting a new remote, run:

```bash
./doc/git-ref-probe.sh <remote-url>
```

It creates `refs/meads/probe/*`, exercises all four properties — including the
raw falsified-old-oid CAS test and its positive control — and deletes the refs
on exit. Output is one `PASS`/`FAIL` line per property; exit status is non-zero
if any failed:

```
1+2. custom namespace + blob-valued ref
  PASS  pushed refs/meads/probe/target -> blob
3. advertised and fetchable
  PASS  advertised in ls-remote
  PASS  round-tripped into a fresh clone, target type is blob
4. server-enforced compare-and-swap
  PASS  false old-oid rejected, ref unchanged
  PASS  positive control: true old-oid accepted, ref moved

result: 5 passed, 0 failed
```

The CAS probe speaks HTTP directly rather than going through git, so it needs
credentials of its own. It tries, in order: `GIT_PASSWORD` / `GITHUB_TOKEN` from
the environment (with `GIT_USERNAME`, default `x-access-token`), then
`git credential fill` — which picks up any configured credential helper — then
`~/.netrc`. If none yield a password the CAS check reports `SKIP` rather than a
false pass.

That probe also requires an **HTTPS** URL; the raw protocol test cannot be done
over SSH. Probing over HTTPS and then using the remote over SSH is fine — the
server-side ref update logic is the same. To probe a GitHub remote you normally
use over SSH:

```bash
GIT_PASSWORD=$(gh auth token) ./doc/git-ref-probe.sh https://github.com/<owner>/<repo>.git
```

The script itself was run against both hosts in the results table above, each
reporting 5/5, with the probe refs confirmed gone from both remotes afterwards.

## Not tested

- **GitLab**, **Bitbucket** — untested; no reason to assume either behaves like
  GitHub.
- **Gerrit** — the most likely to break. It reserves `refs/for/*` and
  `refs/changes/*` and applies per-namespace ACLs, so `refs/meads/*` may be
  refused outright or require explicit project config.
- **Ref pruning / GC** — not tested whether any host garbage-collects blobs
  reachable only from a custom-namespace ref, or expires such refs over time.
  This is worth probing before relying on refs as the *only* copy of task data.
- **Scale** — all tests used a handful of refs. Behaviour with thousands of task
  refs (advertisement size, `ls-remote` cost, push latency) is unmeasured.
